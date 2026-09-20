package service

import (
	"context"
	"errors"
	"testing"

	"github.com/blueship581/veterinary-lab-result-review/backend/internal/constants"
	"github.com/blueship581/veterinary-lab-result-review/backend/internal/dto"
	"github.com/blueship581/veterinary-lab-result-review/backend/internal/model"
	"github.com/blueship581/veterinary-lab-result-review/backend/internal/repository"
)

// signSignedFixture drives a signoff through draft -> peer_review -> signed,
// returning the signed aggregate ready for correction-review scenarios.
func signSignedFixture(t *testing.T, ctx context.Context, svc ResultSignoffService, code string) model.ResultSignoff {
	t.Helper()
	created, err := svc.Create(ctx, signoffInput(code), "operator", "review-fixture-create")
	if err != nil {
		t.Fatalf("create signoff: %v", err)
	}
	updatedInput := updateSignoffInput(created)
	updatedInput.Evidence = "PCR run sheet and control chart fixture revision 2"
	updated, err := svc.Update(ctx, created.ID, updatedInput, "operator", "review-fixture-update")
	if err != nil {
		t.Fatalf("edit signoff draft: %v", err)
	}
	submitted, err := svc.Transition(ctx, updated.ID, dto.TransitionRequest{
		Status: "peer_review", ExpectedVersion: updated.Version, Reason: "evidence complete",
	}, "operator", model.RoleOperator, "review-fixture-submit")
	if err != nil {
		t.Fatalf("submit signoff: %v", err)
	}
	signed, err := svc.Transition(ctx, submitted.ID, dto.TransitionRequest{
		Status: "signed", ExpectedVersion: submitted.Version, Reason: "independent review passed",
	}, "reviewer", model.RoleReviewer, "review-fixture-sign")
	if err != nil {
		t.Fatalf("sign signoff: %v", err)
	}
	return signed
}

func TestCorrectionReviewOpenRequiresEvidenceAndDifferentReviewer(t *testing.T) {
	db := newSignoffTestDB(t)
	svc := NewResultSignoffService(repository.NewResultSignoffRepository(db), nil)
	ctx := context.Background()
	signed := signSignedFixture(t, ctx, svc, "SIGNOFF-CORR-01")

	openInput := dto.OpenSignoffReview{Reason: "post-release metric discrepancy found", Evidence: "re-run sheet 77 shows deviation"}

	// operator role cannot open correction reviews.
	if _, err := svc.OpenReview(ctx, signed.ID, openInput, "operator", model.RoleOperator, "corr-open-denied-role"); !errors.Is(err, ErrReviewRequired) {
		t.Fatalf("operator opening review must be denied, got %v", err)
	}
	// the original signer cannot review their own signoff.
	if _, err := svc.OpenReview(ctx, signed.ID, openInput, "reviewer", model.RoleReviewer, "corr-open-denied-same"); !errors.Is(err, ErrReviewerSeparation) {
		t.Fatalf("same signer opening review must be denied, got %v", err)
	}
	// reason and evidence are mandatory.
	if _, err := svc.OpenReview(ctx, signed.ID, dto.OpenSignoffReview{Reason: "x", Evidence: "   "}, "admin", model.RoleAdmin, "corr-open-denied-input"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing evidence must be denied, got %v", err)
	}
	// only signed records accept correction reviews.
	draft, err := svc.Create(ctx, signoffInput("SIGNOFF-CORR-01-D"), "operator", "corr-draft-create")
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if _, err := svc.OpenReview(ctx, draft.ID, openInput, "admin", model.RoleAdmin, "corr-open-denied-state"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("non-signed record must reject review, got %v", err)
	}

	opened, err := svc.OpenReview(ctx, signed.ID, openInput, "admin", model.RoleAdmin, "corr-open-1")
	if err != nil {
		t.Fatalf("admin opens review: %v", err)
	}
	if len(opened.Reviews) != 1 {
		t.Fatalf("expected one review, got %d", len(opened.Reviews))
	}
	review := opened.Reviews[0]
	if review.Status != string(constants.SignoffReviewOpen) || review.OpenedBy != "admin" || review.OpenSlot == nil || !*review.OpenSlot {
		t.Fatalf("unexpected open review: %#v", review)
	}
	if opened.Status != "signed" || opened.ReviewedBy != "reviewer" {
		t.Fatalf("original signed result must be unchanged after review open: %#v", opened)
	}

	// duplicate requests (even from a different user) collapse onto the existing review.
	if _, err := svc.OpenReview(ctx, signed.ID, openInput, "reviewer2", model.RoleReviewer, "corr-open-duplicate"); !errors.Is(err, ErrReviewOpen) {
		t.Fatalf("duplicate open must return ErrReviewOpen, got %v", err)
	}
}

func TestCorrectionReviewUpholdCreatesLinkedDraftAndKeepsOldVersion(t *testing.T) {
	db := newSignoffTestDB(t)
	svc := NewResultSignoffService(repository.NewResultSignoffRepository(db), nil)
	ctx := context.Background()
	signed := signSignedFixture(t, ctx, svc, "SIGNOFF-CORR-02")

	opened, err := svc.OpenReview(ctx, signed.ID, dto.OpenSignoffReview{
		Reason: "control chart re-evaluation required", Evidence: "QC trace 042 contradicts signed value",
	}, "admin", model.RoleAdmin, "corr-up-open")
	if err != nil {
		t.Fatalf("open review: %v", err)
	}
	review := opened.Reviews[0]

	// the original signer cannot decide either.
	decision := dto.DecideSignoffReview{ReviewID: review.ID, Decision: "upheld", Reason: "deviation confirmed, rework draft", ExpectedVersion: review.ID}
	if _, err := svc.ResolveReview(ctx, signed.ID, decision, "reviewer", model.RoleReviewer, "corr-up-decide-same"); !errors.Is(err, ErrReviewerSeparation) {
		t.Fatalf("original signer deciding must be denied, got %v", err)
	}
	// operator role cannot decide.
	if _, err := svc.ResolveReview(ctx, signed.ID, decision, "operator", model.RoleOperator, "corr-up-decide-role"); !errors.Is(err, ErrReviewRequired) {
		t.Fatalf("operator deciding must be denied, got %v", err)
	}
	// unknown / closed review ids are rejected.
	badDecision := decision
	badDecision.ReviewID = review.ID + 999
	badDecision.ExpectedVersion = badDecision.ReviewID
	if _, err := svc.ResolveReview(ctx, signed.ID, badDecision, "admin", model.RoleAdmin, "corr-up-decide-missing"); !errors.Is(err, ErrReviewNotOpen) {
		t.Fatalf("missing review must return ErrReviewNotOpen, got %v", err)
	}

	upheld, err := svc.ResolveReview(ctx, signed.ID, decision, "admin", model.RoleAdmin, "corr-up-decide")
	if err != nil {
		t.Fatalf("uphold review: %v", err)
	}
	if upheld.Status != "signed" || upheld.Version != signed.Version || upheld.ReviewedBy != "reviewer" {
		t.Fatalf("old signed result must remain unchanged after uphold: %#v", upheld)
	}
	if len(upheld.Reviews) != 1 || upheld.Reviews[0].Status != "upheld" || upheld.Reviews[0].DecidedBy != "admin" || upheld.Reviews[0].OpenSlot != nil {
		t.Fatalf("review must be closed after uphold: %#v", upheld.Reviews)
	}
	if upheld.Reviews[0].DraftID == nil {
		t.Fatalf("upheld review must link the new draft")
	}
	if len(upheld.CorrectionDrafts) != 1 {
		t.Fatalf("expected one linked correction draft, got %d", len(upheld.CorrectionDrafts))
	}

	draft := upheld.CorrectionDrafts[0]
	if draft.Status != "draft" || draft.Version != 1 || draft.PreparedBy != "admin" {
		t.Fatalf("linked correction draft should start as v1 owned by decider: %#v", draft)
	}
	if draft.CorrectionOfID == nil || *draft.CorrectionOfID != signed.ID {
		t.Fatalf("correction draft must point at the original signoff: %#v", draft)
	}
	if draft.MetricValue != signed.MetricValue || draft.Facility != signed.Facility || draft.RiskLevel != signed.RiskLevel {
		t.Fatalf("correction draft must copy signed business fields: %#v vs %#v", draft, signed)
	}

	// fetch the draft directly and confirm it carries its own v1 revision and points back at the original.
	draftAggregate, err := svc.Get(ctx, draft.ID)
	if err != nil {
		t.Fatalf("get correction draft: %v", err)
	}
	if len(draftAggregate.Revisions) != 1 || draftAggregate.Revisions[0].Action != "correction" || draftAggregate.Revisions[0].RequestID != "corr-up-decide" {
		t.Fatalf("correction draft needs v1 evidence revision with the decision request id: %#v", draftAggregate.Revisions)
	}
	if draftAggregate.CorrectionSource == nil || draftAggregate.CorrectionSource.ID != signed.ID || draftAggregate.CorrectionSource.Status != "signed" {
		t.Fatalf("new draft must expose the queryable original signed version: %#v", draftAggregate.CorrectionSource)
	}
	// the original signer is a different person from the new preparer, so they may sign the rework.
	submitted, err := svc.Transition(ctx, draftAggregate.ID, dto.TransitionRequest{
		Status: "peer_review", ExpectedVersion: 1, Reason: "corrected evidence attached",
	}, "admin", model.RoleAdmin, "corr-draft-submit")
	if err != nil {
		t.Fatalf("submit correction draft: %v", err)
	}
	resigned, err := svc.Transition(ctx, submitted.ID, dto.TransitionRequest{
		Status: "signed", ExpectedVersion: submitted.Version, Reason: "corrected result independently verified",
	}, "reviewer", model.RoleReviewer, "corr-draft-sign")
	if err != nil {
		t.Fatalf("re-sign correction draft by a different user: %v", err)
	}
	if resigned.Status != "signed" || resigned.PreparedBy != "admin" || resigned.ReviewedBy != "reviewer" {
		t.Fatalf("correction draft should be signed by a different person than its preparer: %#v", resigned)
	}

	// old version is still queryable, untouched, and still links to the new chain.
	old, err := svc.Get(ctx, signed.ID)
	if err != nil {
		t.Fatalf("get old signed result: %v", err)
	}
	if old.Status != "signed" || old.Version != signed.Version || len(old.Revisions) != 4 {
		t.Fatalf("old version must remain signed with its original 4 revisions: %#v", old)
	}

	// a second upheld cycle on the same signed record must spawn another,
	// uniquely-coded correction draft without colliding with the first one.
	reopened, err := svc.OpenReview(ctx, signed.ID, dto.OpenSignoffReview{
		Reason: "another discrepancy surfaced after rework", Evidence: "second investigation file attached",
	}, "admin", model.RoleAdmin, "corr-up2-open")
	if err != nil {
		t.Fatalf("open second review cycle: %v", err)
	}
	secondReview := reopened.Reviews[len(reopened.Reviews)-1]
	upheldAgain, err := svc.ResolveReview(ctx, signed.ID, dto.DecideSignoffReview{
		ReviewID: secondReview.ID, Decision: "upheld", Reason: "second issue confirmed, another draft needed", ExpectedVersion: secondReview.ID,
	}, "admin", model.RoleAdmin, "corr-up2-decide")
	if err != nil {
		t.Fatalf("second uphold must succeed with a unique draft code: %v", err)
	}
	if len(upheldAgain.CorrectionDrafts) != 2 {
		t.Fatalf("expected two correction drafts after two upheld cycles, got %d", len(upheldAgain.CorrectionDrafts))
	}
	if upheldAgain.CorrectionDrafts[0].Code == upheldAgain.CorrectionDrafts[1].Code {
		t.Fatalf("correction draft codes must be unique across cycles: %s", upheldAgain.CorrectionDrafts[0].Code)
	}
}

func TestCorrectionReviewRejectOnlyClosesReview(t *testing.T) {
	db := newSignoffTestDB(t)
	svc := NewResultSignoffService(repository.NewResultSignoffRepository(db), nil)
	ctx := context.Background()
	signed := signSignedFixture(t, ctx, svc, "SIGNOFF-CORR-03")

	opened, err := svc.OpenReview(ctx, signed.ID, dto.OpenSignoffReview{
		Reason: "suspected transcription error", Evidence: "external complaint attached",
	}, "admin", model.RoleAdmin, "corr-rj-open")
	if err != nil {
		t.Fatalf("open review: %v", err)
	}
	review := opened.Reviews[0]

	rejected, err := svc.ResolveReview(ctx, signed.ID, dto.DecideSignoffReview{
		ReviewID: review.ID, Decision: "rejected", Reason: "re-check confirms signed value is correct", ExpectedVersion: review.ID,
	}, "admin", model.RoleAdmin, "corr-rj-decide")
	if err != nil {
		t.Fatalf("reject review: %v", err)
	}
	if rejected.Status != "signed" || rejected.Version != signed.Version {
		t.Fatalf("rejection must leave the signed result untouched: %#v", rejected)
	}
	if rejected.Reviews[0].Status != "rejected" || rejected.Reviews[0].DraftID != nil {
		t.Fatalf("rejected review must be closed with no linked draft: %#v", rejected.Reviews[0])
	}
	if len(rejected.CorrectionDrafts) != 0 {
		t.Fatalf("rejection must not spawn correction drafts: %#v", rejected.CorrectionDrafts)
	}

	// double-deciding the same review fails with ErrReviewNotOpen.
	if _, err := svc.ResolveReview(ctx, signed.ID, dto.DecideSignoffReview{
		ReviewID: review.ID, Decision: "upheld", Reason: "late duplicate decision", ExpectedVersion: review.ID,
	}, "admin", model.RoleAdmin, "corr-rj-double"); !errors.Is(err, ErrReviewNotOpen) {
		t.Fatalf("closed review cannot be decided twice, got %v", err)
	}
	// after a closed review a fresh correction cycle may start on the same record.
	reopened, err := svc.OpenReview(ctx, signed.ID, dto.OpenSignoffReview{
		Reason: "new evidence surfaced later", Evidence: "second round of QC documentation",
	}, "admin", model.RoleAdmin, "corr-rj-reopen")
	if err != nil {
		t.Fatalf("a new review cycle should be allowed after closure: %v", err)
	}
	if len(reopened.Reviews) != 2 || reopened.Reviews[1].Status != "open" {
		t.Fatalf("expected closed + open review history, got %#v", reopened.Reviews)
	}
}
