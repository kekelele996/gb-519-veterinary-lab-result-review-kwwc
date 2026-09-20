package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueship581/veterinary-lab-result-review/backend/internal/dto"
	"github.com/blueship581/veterinary-lab-result-review/backend/internal/model"
	"github.com/blueship581/veterinary-lab-result-review/backend/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestResultSignoffPreservesVersionsAndRequiresIndependentReviewer(t *testing.T) {
	db := newSignoffTestDB(t)
	svc := NewResultSignoffService(repository.NewResultSignoffRepository(db), nil)
	ctx := context.Background()

	created, err := svc.Create(ctx, signoffInput("SIGNOFF-TEST-01"), "operator", "signoff-create-1")
	if err != nil {
		t.Fatalf("create signoff: %v", err)
	}
	if created.Version != 1 || created.PreparedBy != "operator" || len(created.Revisions) != 1 {
		t.Fatalf("unexpected initial signoff: %#v", created)
	}

	updatedInput := updateSignoffInput(created)
	updatedInput.Evidence = "PCR run sheet and control chart revision 2"
	updated, err := svc.Update(ctx, created.ID, updatedInput, "operator", "signoff-update-2")
	if err != nil {
		t.Fatalf("update draft: %v", err)
	}
	if updated.Version != 2 || len(updated.Revisions) != 2 || updated.Revisions[0].Evidence == updated.Revisions[1].Evidence {
		t.Fatalf("draft versions were not preserved: %#v", updated.Revisions)
	}

	peerReview, err := svc.Transition(ctx, updated.ID, dto.TransitionRequest{
		Status: "peer_review", ExpectedVersion: updated.Version, Reason: "result evidence complete",
	}, "operator", model.RoleOperator, "signoff-submit-3")
	if err != nil {
		t.Fatalf("submit peer review: %v", err)
	}
	if peerReview.Status != "peer_review" || peerReview.Version != 3 || len(peerReview.Revisions) != 3 {
		t.Fatalf("unexpected peer review version: %#v", peerReview)
	}

	decision := dto.TransitionRequest{Status: "signed", ExpectedVersion: peerReview.Version, Reason: "independent laboratory review passed"}
	if _, err := svc.Transition(ctx, peerReview.ID, decision, "operator", model.RoleOperator, "signoff-operator-denied"); !errors.Is(err, ErrReviewRequired) {
		t.Fatalf("operator signing must require reviewer role, got %v", err)
	}
	if _, err := svc.Transition(ctx, peerReview.ID, decision, "operator", model.RoleReviewer, "signoff-same-user-denied"); !errors.Is(err, ErrSeparationOfDuty) {
		t.Fatalf("same preparer and reviewer must be rejected, got %v", err)
	}

	signed, err := svc.Transition(ctx, peerReview.ID, decision, "reviewer", model.RoleReviewer, "signoff-sign-4")
	if err != nil {
		t.Fatalf("sign result: %v", err)
	}
	if signed.Status != "signed" || signed.Version != 4 || signed.ReviewedBy != "reviewer" || len(signed.Revisions) != 4 {
		t.Fatalf("unexpected signed result: %#v", signed)
	}
	for index, revision := range signed.Revisions {
		if revision.Evidence == "" || revision.Actor == "" || revision.RequestID == "" {
			t.Fatalf("revision %d lost attribution or evidence: %#v", index, revision)
		}
	}
	if signed.Revisions[0].RequestID != "signoff-create-1" || signed.Revisions[1].RequestID != "signoff-update-2" ||
		signed.Revisions[2].RequestID != "signoff-submit-3" || signed.Revisions[3].RequestID != "signoff-sign-4" {
		t.Fatalf("request ID chain is incomplete: %#v", signed.Revisions)
	}

	lateUpdate := updateSignoffInput(signed)
	if _, err := svc.Update(ctx, signed.ID, lateUpdate, "operator", "signoff-late-update"); !errors.Is(err, ErrLocked) {
		t.Fatalf("signed result must be immutable, got %v", err)
	}

	var auditCount int64
	if err := db.Model(&model.AuditLog{}).Where("entity_type = ? AND entity_id = ?", "ResultSignoff", signed.ID).Count(&auditCount).Error; err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if auditCount != 4 {
		t.Fatalf("expected 4 atomic audits, got %d", auditCount)
	}
}

// signSigned creates a record driven through to signed by an independent reviewer.
func signSigned(t *testing.T, ctx context.Context, svc ResultSignoffService, code, preparer, reviewer string) model.ResultSignoff {
	t.Helper()
	created, err := svc.Create(ctx, signoffInput(code), preparer, code+"-create")
	if err != nil {
		t.Fatalf("create %s: %v", code, err)
	}
	updated, err := svc.Update(ctx, created.ID, updateSignoffInput(created), preparer, code+"-update")
	if err != nil {
		t.Fatalf("update %s: %v", code, err)
	}
	peerReview, err := svc.Transition(ctx, updated.ID, dto.TransitionRequest{
		Status: "peer_review", ExpectedVersion: updated.Version, Reason: "submit for independent review",
	}, preparer, model.RoleOperator, code+"-submit")
	if err != nil {
		t.Fatalf("submit %s: %v", code, err)
	}
	signed, err := svc.Transition(ctx, peerReview.ID, dto.TransitionRequest{
		Status: "signed", ExpectedVersion: peerReview.Version, Reason: "independent review passed",
	}, reviewer, model.RoleReviewer, code+"-signed")
	if err != nil {
		t.Fatalf("sign %s: %v", code, err)
	}
	return signed
}

func TestCorrectionReviewApproveCreatesDraftAndSupersedesOriginal(t *testing.T) {
	db := newSignoffTestDB(t)
	svc := NewResultSignoffService(repository.NewResultSignoffRepository(db), nil)
	ctx := context.Background()

	original := signSigned(t, ctx, svc, "CORRECTION-APPROVE-01", "operator", "reviewer")

	// operator may not open a correction; the original signer also may not.
	if _, err := svc.OpenCorrection(ctx, original.ID, dto.OpenCorrectionRequest{
		Reason: "control value disputed", Evidence: "re-run QC chart attached",
	}, "operator", model.RoleOperator, "corr-open-operator"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("operator must not open corrections, got %v", err)
	}
	if _, err := svc.OpenCorrection(ctx, original.ID, dto.OpenCorrectionRequest{
		Reason: "control value disputed", Evidence: "re-run QC chart attached",
	}, "reviewer", model.RoleReviewer, "corr-open-signer"); !errors.Is(err, ErrSeparationOfDuty) {
		t.Fatalf("original reviewer must not correct their own signoff, got %v", err)
	}

	openInput := dto.OpenCorrectionRequest{Reason: "control value disputed by second assay", Evidence: "re-run QC chart and sample trace attached"}
	correction, err := svc.OpenCorrection(ctx, original.ID, openInput, "admin", model.RoleAdmin, "corr-open-1")
	if err != nil {
		t.Fatalf("admin opens correction: %v", err)
	}
	if correction.Status != model.SignoffCorrectionStatusOpen || correction.RequestedBy != "admin" {
		t.Fatalf("unexpected open correction: %#v", correction)
	}

	// Duplicate / concurrent open keeps only one review.
	if _, err := svc.OpenCorrection(ctx, original.ID, openInput, "admin", model.RoleAdmin, "corr-open-dup"); !errors.Is(err, ErrCorrectionConflict) {
		t.Fatalf("duplicate open must conflict, got %v", err)
	}
	// any other independent reviewer racing the same signed record is also single-flighted
	if _, err := svc.OpenCorrection(ctx, original.ID, openInput, "reviewer2b", model.RoleReviewer, "corr-open-race2"); !errors.Is(err, ErrCorrectionConflict) {
		t.Fatalf("concurrent open must conflict, got %v", err)
	}
	// the original signer is blocked by separation of duties even against the open review
	if _, err := svc.OpenCorrection(ctx, original.ID, openInput, "reviewer", model.RoleReviewer, "corr-open-signer-again"); !errors.Is(err, ErrSeparationOfDuty) {
		t.Fatalf("original signer must not open a competing correction, got %v", err)
	}

	// reason/evidence are required
	if _, err := svc.OpenCorrection(ctx, original.ID, dto.OpenCorrectionRequest{Reason: "x", Evidence: ""}, "admin2", model.RoleAdmin, "corr-open-invalid"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing evidence must be rejected, got %v", err)
	}

	// cannot decide someone else's signoff mismatch: original signer cannot decide
	if _, err := svc.DecideCorrection(ctx, original.ID, correction.ID, dto.CorrectionDecisionRequest{Approve: true},
		"reviewer", model.RoleReviewer, "corr-decide-signer"); !errors.Is(err, ErrSeparationOfDuty) {
		t.Fatalf("original signer must not decide the correction, got %v", err)
	}
	// deciding a correction id that does not exist surfaces not-found
	if _, err := svc.DecideCorrection(ctx, original.ID, correction.ID+999, dto.CorrectionDecisionRequest{Approve: true},
		"admin", model.RoleAdmin, "corr-decide-missing"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing correction must be not found, got %v", err)
	}

	decided, err := svc.DecideCorrection(ctx, original.ID, correction.ID, dto.CorrectionDecisionRequest{
		Approve: true, DecisionNote: "evidence supports reissuing the result",
	}, "admin", model.RoleAdmin, "corr-approve")
	if err != nil {
		t.Fatalf("approve correction: %v", err)
	}
	if decided.Status != model.SignoffCorrectionStatusApproved || decided.DraftSignoffID == nil || decided.DecidedBy != "admin" {
		t.Fatalf("approved correction must link a draft: %#v", decided)
	}

	// A second decision on the now closed review fails.
	if _, err := svc.DecideCorrection(ctx, original.ID, correction.ID, dto.CorrectionDecisionRequest{Approve: false},
		"admin", model.RoleAdmin, "corr-decide-twice"); !errors.Is(err, ErrCorrectionNotOpen) {
		t.Fatalf("closed correction must reject a second decision, got %v", err)
	}

	// The original signed result stays unchanged.
	originalAfter, err := svc.Get(ctx, original.ID)
	if err != nil {
		t.Fatalf("reload original: %v", err)
	}
	if originalAfter.Status != "signed" || originalAfter.Superseded || originalAfter.Version != original.Version {
		t.Fatalf("original signed result must be unchanged after approval: %#v", originalAfter)
	}

	// The linked draft copies the signed result, starts at v1 prepared by the approver.
	draft, err := svc.Get(ctx, *decided.DraftSignoffID)
	if err != nil {
		t.Fatalf("load correction draft: %v", err)
	}
	if draft.Status != "draft" || draft.Version != 1 || draft.PreparedBy != "admin" {
		t.Fatalf("unexpected correction draft: %#v", draft)
	}
	if draft.CorrectionOfID == nil || *draft.CorrectionOfID != original.ID {
		t.Fatalf("draft must link back to the original signed record: %#v", draft)
	}
	if draft.MetricValue != original.MetricValue || draft.RelatedCode != original.Code {
		t.Fatalf("draft must copy the signed result fields: %#v", draft)
	}

	// New draft is re-submitted and signed by yet another different user, then it takes effect.
	peerReview, err := svc.Transition(ctx, draft.ID, dto.TransitionRequest{
		Status: "peer_review", ExpectedVersion: draft.Version, Reason: "corrected result re-submitted",
	}, "admin", model.RoleAdmin, "corr-draft-submit")
	if err != nil {
		t.Fatalf("submit correction draft: %v", err)
	}
	resigned, err := svc.Transition(ctx, draft.ID, dto.TransitionRequest{
		Status: "signed", ExpectedVersion: peerReview.Version, Reason: "independent review of corrected result",
	}, "reviewer2b", model.RoleReviewer, "corr-draft-signed")
	if err != nil {
		t.Fatalf("sign corrected result by different user: %v", err)
	}
	if resigned.Status != "signed" || resigned.ReviewedBy != "reviewer2b" {
		t.Fatalf("corrected draft must be signed by a different user: %#v", resigned)
	}

	originalFinal, err := svc.Get(ctx, original.ID)
	if err != nil {
		t.Fatalf("reload original after correction signed: %v", err)
	}
	if !originalFinal.Superseded || originalFinal.Status != "signed" {
		t.Fatalf("original must remain signed but be superseded: %#v", originalFinal)
	}
	if originalFinal.Version != original.Version {
		t.Fatalf("original version chain must not be rewritten: got %d want %d", originalFinal.Version, original.Version)
	}

	// Opening a new correction on the superseded original is rejected.
	if _, err := svc.OpenCorrection(ctx, original.ID, openInput, "reviewer2b", model.RoleReviewer, "corr-open-superseded"); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("superseded signed result must not accept new corrections, got %v", err)
	}

	// Audit trail: original has create/update/submit/sign + one supersede entry.
	var originalAudits int64
	if err := db.Model(&model.AuditLog{}).Where("entity_type = ? AND entity_id = ?", "ResultSignoff", original.ID).Count(&originalAudits).Error; err != nil {
		t.Fatalf("count original audits: %v", err)
	}
	if originalAudits != 5 {
		t.Fatalf("expected 5 audits on original (4 lifecycle + supersede), got %d", originalAudits)
	}
	var correctionAudits int64
	if err := db.Model(&model.AuditLog{}).Where("entity_type = ? AND entity_id = ?", "SignoffCorrection", correction.ID).Count(&correctionAudits).Error; err != nil {
		t.Fatalf("count correction audits: %v", err)
	}
	if correctionAudits != 2 {
		t.Fatalf("expected open+approve correction audits, got %d", correctionAudits)
	}
}

func TestCorrectionReviewRejectLeavesOriginalUntouched(t *testing.T) {
	db := newSignoffTestDB(t)
	svc := NewResultSignoffService(repository.NewResultSignoffRepository(db), nil)
	ctx := context.Background()

	original := signSigned(t, ctx, svc, "CORRECTION-REJECT-01", "operator", "reviewer")
	correction, err := svc.OpenCorrection(ctx, original.ID, dto.OpenCorrectionRequest{
		Reason: "disputed low risk marking", Evidence: "repeat assay panel attached",
	}, "admin", model.RoleAdmin, "corr-rej-open")
	if err != nil {
		t.Fatalf("open correction: %v", err)
	}
	rejected, err := svc.DecideCorrection(ctx, original.ID, correction.ID, dto.CorrectionDecisionRequest{
		Approve: false, DecisionNote: "evidence does not support changing the result",
	}, "admin", model.RoleAdmin, "corr-rej-decide")
	if err != nil {
		t.Fatalf("reject correction: %v", err)
	}
	if rejected.Status != model.SignoffCorrectionStatusRejected || rejected.DraftSignoffID != nil {
		t.Fatalf("rejected correction must not create a draft: %#v", rejected)
	}
	after, err := svc.Get(ctx, original.ID)
	if err != nil {
		t.Fatalf("reload original: %v", err)
	}
	if after.Status != "signed" || after.Superseded || after.Version != original.Version {
		t.Fatalf("rejected correction must leave the signed result unchanged: %#v", after)
	}
	var drafts int64
	if err := db.Model(&model.ResultSignoff{}).Where("correction_of_id = ?", original.ID).Count(&drafts).Error; err != nil {
		t.Fatalf("count linked drafts: %v", err)
	}
	if drafts != 0 {
		t.Fatalf("rejection must not create a correction draft, got %d", drafts)
	}
	// After rejection a fresh review may be opened.
	reopened, err := svc.OpenCorrection(ctx, original.ID, dto.OpenCorrectionRequest{
		Reason: "new evidence arrived after rejection", Evidence: "second repeat panel attached",
	}, "admin", model.RoleAdmin, "corr-rej-reopen")
	if err != nil {
		t.Fatalf("a new correction must be allowed after rejection: %v", err)
	}
	if reopened.Status != model.SignoffCorrectionStatusOpen {
		t.Fatalf("reopened correction must be open: %#v", reopened)
	}
}

func TestCorrectionRejectsNonSignedAndInFlight(t *testing.T) {
	db := newSignoffTestDB(t)
	svc := NewResultSignoffService(repository.NewResultSignoffRepository(db), nil)
	ctx := context.Background()

	created, err := svc.Create(ctx, signoffInput("CORRECTION-GUARD-01"), "operator", "guard-create")
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if _, err := svc.OpenCorrection(ctx, created.ID, dto.OpenCorrectionRequest{
		Reason: "cannot review a draft", Evidence: "n/a",
	}, "admin", model.RoleAdmin, "guard-draft-open"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("only signed records accept corrections, got %v", err)
	}

	original := signSigned(t, ctx, svc, "CORRECTION-GUARD-02", "operator", "reviewer")
	first, err := svc.OpenCorrection(ctx, original.ID, dto.OpenCorrectionRequest{
		Reason: "first dispute", Evidence: "first evidence pack",
	}, "admin", model.RoleAdmin, "guard-first-open")
	if err != nil {
		t.Fatalf("open first correction: %v", err)
	}
	if _, err := svc.DecideCorrection(ctx, original.ID, first.ID, dto.CorrectionDecisionRequest{Approve: true},
		"admin", model.RoleAdmin, "guard-first-approve"); err != nil {
		t.Fatalf("approve first correction: %v", err)
	}
	// An approved, not-yet-signed correction draft blocks another review.
	if _, err := svc.OpenCorrection(ctx, original.ID, dto.OpenCorrectionRequest{
		Reason: "second dispute while draft in flight", Evidence: "second evidence pack",
	}, "admin", model.RoleAdmin, "guard-second-open"); !errors.Is(err, ErrCorrectionConflict) {
		t.Fatalf("in-flight correction draft must block a new review, got %v", err)
	}

	// Once the draft is rejected through the normal signoff flow, the chain is finished
	// but the original stays signed; a new correction is still blocked because an approved
	// correction chain exists only while its draft is non-terminal. Rejecting the draft
	// leaves the original signed and reviewable again.
	firstReload, err := svc.GetCorrection(ctx, first.ID)
	if err != nil {
		t.Fatalf("reload correction: %v", err)
	}
	draft, err := svc.Get(ctx, *firstReload.DraftSignoffID)
	if err != nil {
		t.Fatalf("load draft: %v", err)
	}
	peerReview, err := svc.Transition(ctx, draft.ID, dto.TransitionRequest{
		Status: "peer_review", ExpectedVersion: draft.Version, Reason: "submit corrected result",
	}, "admin", model.RoleAdmin, "guard-draft-submit")
	if err != nil {
		t.Fatalf("submit correction draft: %v", err)
	}
	rejectedDraft, err := svc.Transition(ctx, draft.ID, dto.TransitionRequest{
		Status: "rejected", ExpectedVersion: peerReview.Version, Reason: "corrected result rejected in independent review",
	}, "reviewer2b", model.RoleReviewer, "guard-draft-reject")
	if err != nil {
		t.Fatalf("reject correction draft: %v", err)
	}
	if rejectedDraft.Status != "rejected" {
		t.Fatalf("draft should be rejected: %#v", rejectedDraft)
	}
	originalAfter, err := svc.Get(ctx, original.ID)
	if err != nil {
		t.Fatalf("reload original: %v", err)
	}
	if originalAfter.Superseded {
		t.Fatalf("a rejected correction must not supersede the original signed result")
	}
	again, err := svc.OpenCorrection(ctx, original.ID, dto.OpenCorrectionRequest{
		Reason: "third dispute after correction draft rejected", Evidence: "third evidence pack",
	}, "admin", model.RoleAdmin, "guard-third-open")
	if err != nil {
		t.Fatalf("a new correction must be allowed once the correction draft is rejected: %v", err)
	}
	if again.Status != model.SignoffCorrectionStatusOpen {
		t.Fatalf("new correction should be open: %#v", again)
	}
}

var signoffTestDBSuffix int64

func newSignoffTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	suffix := atomic.AddInt64(&signoffTestDBSuffix, 1)
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), suffix)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.AuditLog{}, &model.ResultSignoff{}, &model.ResultSignoffRevision{}, &model.SignoffCorrection{}); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	return db
}

func signoffInput(code string) dto.CreateResultSignoff {
	return dto.CreateResultSignoff{
		Code: code, Name: "PCR result signoff", Description: "controlled veterinary result",
		Facility: "Veterinary Lab 2", Owner: "Result desk", Category: "PCR", RiskLevel: "high",
		MetricValue: 99.8, MetricUnit: "percent", EffectiveAt: time.Now().UTC(),
		Evidence: "PCR run sheet and control chart revision 1", RelatedCode: "ASSAY-101",
	}
}

func updateSignoffInput(item model.ResultSignoff) dto.UpdateResultSignoff {
	return dto.UpdateResultSignoff{
		ExpectedVersion: item.Version, Name: item.Name, Description: item.Description,
		Facility: item.Facility, Owner: item.Owner, Category: item.Category, RiskLevel: item.RiskLevel,
		MetricValue: item.MetricValue, MetricUnit: item.MetricUnit, EffectiveAt: item.EffectiveAt,
		Evidence: item.Evidence, RelatedCode: item.RelatedCode,
	}
}

func TestCorrectionConcurrentOpensKeepExactlyOne(t *testing.T) {
	db := newSignoffTestDB(t)
	svc := NewResultSignoffService(repository.NewResultSignoffRepository(db), nil)
	ctx := context.Background()
	original := signSigned(t, ctx, svc, "CORRECTION-CONCUR-01", "operator", "reviewer")

	const racers = 12
	start := make(chan struct{})
	var wg sync.WaitGroup
	var successCount, conflictCount, otherCount int64
	var otherErrors []string
	var successMu sync.Mutex
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func(index int) {
			defer wg.Done()
			<-start
			_, err := svc.OpenCorrection(ctx, original.ID, dto.OpenCorrectionRequest{
				Reason:   fmt.Sprintf("concurrent dispute number %d", index),
				Evidence: "concurrent evidence pack with supporting chart",
			}, "admin", model.RoleAdmin, fmt.Sprintf("corr-race-%d", index))
			successMu.Lock()
			defer successMu.Unlock()
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			} else if errors.Is(err, ErrCorrectionConflict) {
				atomic.AddInt64(&conflictCount, 1)
			} else {
				atomic.AddInt64(&otherCount, 1)
				otherErrors = append(otherErrors, err.Error())
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if successCount != 1 {
		t.Fatalf("exactly one concurrent open must succeed, got %d (other: %v)", successCount, otherErrors)
	}
	if otherCount != 0 {
		t.Fatalf("losers must receive the conflict error, got %d others: %v", otherCount, otherErrors)
	}
	if conflictCount != racers-1 {
		t.Fatalf("all other concurrent opens must conflict, got %d of %d", conflictCount, racers)
	}
	var stored int64
	if err := db.Model(&model.SignoffCorrection{}).Where("result_signoff_id = ?", original.ID).Count(&stored).Error; err != nil {
		t.Fatalf("count stored corrections: %v", err)
	}
	if stored != 1 {
		t.Fatalf("only one correction row must be persisted, got %d", stored)
	}
}

func TestCorrectionDecisionFailureRollsBack(t *testing.T) {
	db := newSignoffTestDB(t)
	svc := NewResultSignoffService(repository.NewResultSignoffRepository(db), nil)
	ctx := context.Background()
	original := signSigned(t, ctx, svc, "CORRECTION-ROLLBACK-01", "operator", "reviewer")
	correction, err := svc.OpenCorrection(ctx, original.ID, dto.OpenCorrectionRequest{
		Reason: "rollback dispute", Evidence: "rollback evidence pack",
	}, "admin", model.RoleAdmin, "corr-rb-open")
	if err != nil {
		t.Fatalf("open correction: %v", err)
	}

	// A decision against a mismatched signoff id fails before any write and must leave
	// the review open and create no draft.
	if _, err := svc.DecideCorrection(ctx, original.ID+100, correction.ID, dto.CorrectionDecisionRequest{Approve: true},
		"admin", model.RoleAdmin, "corr-rb-mismatch"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("mismatched signoff must be not found, got %v", err)
	}
	reloaded, err := svc.GetCorrection(ctx, correction.ID)
	if err != nil {
		t.Fatalf("reload correction: %v", err)
	}
	if reloaded.Status != model.SignoffCorrectionStatusOpen || reloaded.DraftSignoffID != nil {
		t.Fatalf("failed decision must leave review open with no draft: %#v", reloaded)
	}
	var drafts int64
	if err := db.Model(&model.ResultSignoff{}).Where("correction_of_id = ?", original.ID).Count(&drafts).Error; err != nil {
		t.Fatalf("count drafts: %v", err)
	}
	if drafts != 0 {
		t.Fatalf("failed approval must not leave a draft, got %d", drafts)
	}

	// A second decision on an already approved review fails inside the transaction and
	// must not create a second correction draft.
	if _, err := svc.DecideCorrection(ctx, original.ID, correction.ID, dto.CorrectionDecisionRequest{Approve: true},
		"admin", model.RoleAdmin, "corr-rb-first"); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	if _, err := svc.DecideCorrection(ctx, original.ID, correction.ID, dto.CorrectionDecisionRequest{Approve: true},
		"admin", model.RoleAdmin, "corr-rb-second"); !errors.Is(err, ErrCorrectionNotOpen) {
		t.Fatalf("second decision must fail as not open, got %v", err)
	}
	if err := db.Model(&model.ResultSignoff{}).Where("correction_of_id = ?", original.ID).Count(&drafts).Error; err != nil {
		t.Fatalf("recount drafts: %v", err)
	}
	if drafts != 1 {
		t.Fatalf("exactly one draft must survive a failed second decision, got %d", drafts)
	}
}
