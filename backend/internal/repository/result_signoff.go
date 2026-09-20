package repository

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/blueship581/veterinary-lab-result-review/backend/internal/dto"
	"github.com/blueship581/veterinary-lab-result-review/backend/internal/model"
	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// Sentinel errors for correction-review persistence. The service maps these
// onto its exported business errors.
var (
	ErrDuplicateReview     = errors.New("an in-progress correction review already exists")
	ErrReviewTargetInvalid = errors.New("correction review target must be a signed result")
	ErrReviewMissing       = errors.New("correction review is not open for this record")
)

// ResultSignoffRepository owns all persistence operations for 结果签发.
type ResultSignoffRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.ResultSignoff], error)
	Get(context.Context, uint) (model.ResultSignoff, error)
	CreateVersion(context.Context, *model.ResultSignoff, string, string) error
	UpdateVersion(context.Context, uint, uint, *model.ResultSignoff, string, string, string, string, string) error
	OpenReview(context.Context, *model.SignoffReview, string, string) error
	ResolveReview(ctx context.Context, signoffID, reviewID uint, decision, reason, actor, requestID string) error
	Delete(context.Context, uint) error
	CountByStatus(context.Context) (map[string]int64, error)
}

type resultSignoffRepository struct {
	store *Store[model.ResultSignoff]
	db    *gorm.DB
}

func NewResultSignoffRepository(db *gorm.DB) ResultSignoffRepository {
	return &resultSignoffRepository{store: NewStore[model.ResultSignoff](db), db: db}
}

func (r *resultSignoffRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.ResultSignoff], error) {
	page, err := r.store.List(ctx, q)
	if err != nil || len(page.Items) == 0 {
		return page, err
	}
	items := make([]*model.ResultSignoff, 0, len(page.Items))
	for index := range page.Items {
		items = append(items, &page.Items[index])
	}
	if err := r.hydrate(ctx, r.db, items); err != nil {
		return Page[model.ResultSignoff]{}, err
	}
	return page, nil
}

func (r *resultSignoffRepository) Get(ctx context.Context, id uint) (model.ResultSignoff, error) {
	var item model.ResultSignoff
	if err := r.db.WithContext(ctx).First(&item, id).Error; err != nil {
		return model.ResultSignoff{}, err
	}
	if err := r.hydrate(ctx, r.db, []*model.ResultSignoff{&item}); err != nil {
		return model.ResultSignoff{}, err
	}
	return item, nil
}

// hydrate batch-loads append-only revisions, correction-review work orders,
// correction drafts spawned from each record and the original result behind a
// draft. Batch IN-queries keep list rendering free of N+1 access.
func (r *resultSignoffRepository) hydrate(ctx context.Context, db *gorm.DB, items []*model.ResultSignoff) error {
	ids := make([]uint, 0, len(items))
	sourceIDs := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
		if item.CorrectionOfID != nil {
			sourceIDs = append(sourceIDs, *item.CorrectionOfID)
		}
	}

	var revisions []model.ResultSignoffRevision
	if err := db.WithContext(ctx).Where("result_signoff_id IN ?", ids).
		Order("result_signoff_id, version").Find(&revisions).Error; err != nil {
		return err
	}
	bySignoff := make(map[uint][]model.ResultSignoffRevision)
	for _, revision := range revisions {
		bySignoff[revision.ResultSignoffID] = append(bySignoff[revision.ResultSignoffID], revision)
	}

	var reviews []model.SignoffReview
	if err := db.WithContext(ctx).Where("result_signoff_id IN ?", ids).
		Order("created_at, id").Find(&reviews).Error; err != nil {
		return err
	}
	byReviewTarget := make(map[uint][]model.SignoffReview)
	for _, review := range reviews {
		byReviewTarget[review.ResultSignoffID] = append(byReviewTarget[review.ResultSignoffID], review)
	}

	draftsBySource := make(map[uint][]model.ResultSignoff)
	if len(ids) > 0 {
		var drafts []model.ResultSignoff
		if err := db.WithContext(ctx).Where("correction_of_id IN ?", ids).
			Order("id").Find(&drafts).Error; err != nil {
			return err
		}
		for _, draft := range drafts {
			draftsBySource[*draft.CorrectionOfID] = append(draftsBySource[*draft.CorrectionOfID], draft)
		}
	}

	sources := make(map[uint]model.ResultSignoff)
	if len(sourceIDs) > 0 {
		var sourceRecords []model.ResultSignoff
		if err := db.WithContext(ctx).Where("id IN ?", sourceIDs).
			Find(&sourceRecords).Error; err != nil {
			return err
		}
		for _, source := range sourceRecords {
			sources[source.ID] = source
		}
	}

	for _, item := range items {
		item.Revisions = bySignoff[item.ID]
		item.Reviews = byReviewTarget[item.ID]
		item.CorrectionDrafts = draftsBySource[item.ID]
		if item.CorrectionOfID != nil {
			if source, ok := sources[*item.CorrectionOfID]; ok {
				copy := source
				item.CorrectionSource = &copy
			}
		}
	}
	return nil
}

func (r *resultSignoffRepository) CreateVersion(ctx context.Context, item *model.ResultSignoff, actor, requestID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Revisions", "Reviews", "CorrectionDrafts", "CorrectionSource").Create(item).Error; err != nil {
			return err
		}
		revision := model.ResultSignoffRevision{
			ResultSignoffID: item.ID, Version: item.Version, Status: item.Status,
			Evidence: item.Evidence, Actor: actor, RequestID: requestID, Action: "create",
			Reason: "signoff drafted", CreatedAt: item.CreatedAt,
		}
		if err := tx.Create(&revision).Error; err != nil {
			return err
		}
		return appendAudit(tx, actor, requestID, "create", "ResultSignoff", item.ID, "", item.Status, "signoff version 1 drafted")
	})
}
func (r *resultSignoffRepository) UpdateVersion(ctx context.Context, id, expectedVersion uint, item *model.ResultSignoff, actor, requestID, action, before, reason string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		item.Revisions = nil
		if err := optimisticUpdate(tx, id, expectedVersion, item); err != nil {
			return err
		}
		revision := model.ResultSignoffRevision{
			ResultSignoffID: id, Version: item.Version, Status: item.Status,
			Evidence: item.Evidence, Actor: actor, RequestID: requestID, Action: action,
			Reason: reason, CreatedAt: item.UpdatedAt,
		}
		if err := tx.Create(&revision).Error; err != nil {
			return err
		}
		return appendAudit(tx, actor, requestID, action, "ResultSignoff", id, before, item.Status, reason)
	})
}

// OpenReview inserts the correction-review work order. A composite unique
// index on (result_signoff_id, open_slot) is the cross-database guard that
// collapses duplicate or concurrent open requests onto one surviving row:
// open rows carry open_slot=true, closed rows carry NULL, and MySQL, SQLite
// and PostgreSQL all treat NULLs as distinct so closed history accumulates.
func (r *resultSignoffRepository) OpenReview(ctx context.Context, review *model.SignoffReview, actor, requestID string) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var signoff model.ResultSignoff
		if err := tx.First(&signoff, review.ResultSignoffID).Error; err != nil {
			return err
		}
		if signoff.Status != "signed" {
			return ErrReviewTargetInvalid
		}
		var openCount int64
		if err := tx.Model(&model.SignoffReview{}).
			Where("result_signoff_id = ? AND status = ?", review.ResultSignoffID, "open").
			Count(&openCount).Error; err != nil {
			return err
		}
		if openCount > 0 {
			return ErrDuplicateReview
		}
		if err := tx.Create(review).Error; err != nil {
			if isDuplicateKeyError(err) {
				return ErrDuplicateReview
			}
			return err
		}
		return appendAudit(tx, actor, requestID, "review_open", "ResultSignoff", review.ResultSignoffID, "signed", "review_open",
			"correction review opened: "+review.Reason)
	})
	if err != nil && !errors.Is(err, ErrDuplicateReview) && !errors.Is(err, ErrReviewTargetInvalid) && isDuplicateKeyError(err) {
		return ErrDuplicateReview
	}
	return err
}

// ResolveReview closes an open review inside one transaction. An upheld
// decision copies the signed result into a linked correction draft and writes
// its v1 revision and audit entries before the review is closed; any failure
// rolls the whole decision back. Rejection mutates nothing but the review.
func (r *resultSignoffRepository) ResolveReview(ctx context.Context, signoffID, reviewID uint, decision, reason, actor, requestID string) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.resolveReviewTx(tx, signoffID, reviewID, decision, reason, actor, requestID)
	})
	if err != nil && !errors.Is(err, ErrReviewTargetInvalid) && !errors.Is(err, ErrReviewMissing) &&
		!errors.Is(err, ErrDuplicateReview) && !errors.Is(err, ErrVersionConflict) && isDuplicateKeyError(err) {
		// Lost a concurrent decision race; nothing was committed.
		return ErrVersionConflict
	}
	return err
}

func (r *resultSignoffRepository) resolveReviewTx(tx *gorm.DB, signoffID, reviewID uint, decision, reason, actor, requestID string) error {
	var signoff model.ResultSignoff
	if err := tx.First(&signoff, signoffID).Error; err != nil {
		return err
	}
	if signoff.Status != "signed" {
		return ErrReviewTargetInvalid
	}
	var review model.SignoffReview
	if err := tx.Where("id = ? AND result_signoff_id = ?", reviewID, signoffID).First(&review).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrReviewMissing
		}
		return err
	}
	if review.Status != "open" {
		return ErrReviewMissing
	}

	now := time.Now().UTC()
	updates := map[string]any{
		"status": decision, "open_slot": nil, "decided_by": actor,
		"decision_reason": reason, "decided_at": now,
	}
	auditAction := "review_reject"
	afterState := "review_rejected"
	detail := "correction review rejected, signed result unchanged: " + reason

	if decision == "upheld" {
		draft := buildCorrectionDraft(&signoff, review.ID, actor, now)
		if err := tx.Omit("Revisions", "Reviews", "CorrectionDrafts", "CorrectionSource").Create(&draft).Error; err != nil {
			if isDuplicateKeyError(err) {
				return ErrDuplicateReview
			}
			return err
		}
		evidence := signoff.Evidence
		if strings.TrimSpace(evidence) == "" {
			evidence = review.Evidence
		}
		revision := model.ResultSignoffRevision{
			ResultSignoffID: draft.ID, Version: 1, Status: "draft",
			Evidence: evidence, Actor: actor, RequestID: requestID, Action: "correction",
			Reason: reason, CreatedAt: now,
		}
		if err := tx.Create(&revision).Error; err != nil {
			return err
		}
		if err := appendAudit(tx, actor, requestID, "correction_create", "ResultSignoff", draft.ID, "", "draft",
			"correction draft created from signed "+signoff.Code+": "+reason); err != nil {
			return err
		}
		updates["draft_id"] = draft.ID
		auditAction = "review_uphold"
		afterState = "review_upheld"
		detail = "correction review upheld, linked draft " + draft.Code + ": " + reason
	}

	result := tx.Model(&model.SignoffReview{}).
		Where("id = ? AND result_signoff_id = ? AND status = ?", reviewID, signoffID, "open").
		Updates(updates)
	if result.Error != nil {
		if isDuplicateKeyError(result.Error) {
			return ErrVersionConflict
		}
		return result.Error
	}
	if result.RowsAffected == 0 {
		// A concurrent decision closed the review first; roll the draft
		// creation back as well.
		return ErrVersionConflict
	}
	return appendAudit(tx, actor, requestID, auditAction, "ResultSignoff", signoffID, "review_open", afterState, detail)
}

// buildCorrectionDraft copies the immutable signed result into a fresh v1
// draft. The review id is part of the code so multiple upheld cycles on one
// signed record never collide. The decider owns preparation so the eventual
// re-sign must be a different person, and strings are column-bounded.
func buildCorrectionDraft(signoff *model.ResultSignoff, reviewID uint, actor string, now time.Time) model.ResultSignoff {
	baseCode := trimRunes(signoff.Code, 46)
	return model.ResultSignoff{
		BaseModel: model.BaseModel{
			Code:        baseCode + "-S" + strconv.FormatUint(uint64(signoff.ID), 10) + "V" + strconv.FormatUint(uint64(reviewID), 10) + "-DRAFT",
			Name:        trimRunes(signoff.Name, 154) + "（更正草稿）",
			Status:      model.ResultSignoffInitialStatus,
			Version:     1,
			Description: trimRunes(signoff.Description, 1000),
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		Facility:       signoff.Facility,
		Owner:          signoff.Owner,
		Category:       signoff.Category,
		RiskLevel:      signoff.RiskLevel,
		MetricValue:    signoff.MetricValue,
		MetricUnit:     signoff.MetricUnit,
		EffectiveAt:    signoff.EffectiveAt,
		Evidence:       trimRunes(signoff.Evidence, 2000),
		RelatedCode:    signoff.Code,
		PreparedBy:     actor,
		CorrectionOfID: &signoff.ID,
	}
}

func trimRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(string(runes[:limit]))
}

func isDuplicateKeyError(err error) bool {
	var mysqlError *mysql.MySQLError
	if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
		return true
	}
	text := err.Error()
	if strings.Contains(text, "UNIQUE constraint failed") {
		return true
	}
	// SQLite serializes writers; a busy transaction lost the race against the
	// single allowed open review and is reported back as a conflict so callers
	// refresh instead of seeing a 500. MySQL returns the 1062 duplicate above.
	if strings.Contains(text, "database is locked") || strings.Contains(text, "SQLITE_BUSY") {
		return true
	}
	return false
}

func (r *resultSignoffRepository) Delete(ctx context.Context, id uint) error {
	return r.store.Delete(ctx, id)
}
func (r *resultSignoffRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}
