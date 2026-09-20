package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/blueship581/veterinary-lab-result-review/backend/internal/dto"
	"github.com/blueship581/veterinary-lab-result-review/backend/internal/model"
	"gorm.io/gorm"
)

// ResultSignoffRepository owns all persistence operations for 结果签发.
type ResultSignoffRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.ResultSignoff], error)
	Get(context.Context, uint) (model.ResultSignoff, error)
	CreateVersion(context.Context, *model.ResultSignoff, string, string) error
	UpdateVersion(context.Context, uint, uint, *model.ResultSignoff, string, string, string, string, string, uint) error
	Delete(context.Context, uint) error
	CountByStatus(context.Context) (map[string]int64, error)

	Transaction(context.Context, func(tx *gorm.DB) error) error
	CreateVersionTx(tx *gorm.DB, item *model.ResultSignoff, actor, requestID, action, reason string) error
	MarkSupersededTx(tx *gorm.DB, originalID uint, actor, requestID, newCode string) error

	CreateCorrection(context.Context, *model.SignoffCorrection) error
	GetCorrection(context.Context, uint) (model.SignoffCorrection, error)
	FindOpenCorrection(context.Context, uint) (model.SignoffCorrection, error)
	OpenCorrectionDraftID(context.Context, uint) (uint, error)
	DecideCorrectionTx(tx *gorm.DB, correctionID uint, status, decidedBy, decisionNote, actor, requestID string) error
	LinkCorrectionDraftTx(tx *gorm.DB, correctionID, draftID uint) error
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
	ids := make([]uint, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.ID)
	}
	var revisions []model.ResultSignoffRevision
	if err := r.db.WithContext(ctx).Where("result_signoff_id IN ?", ids).
		Order("result_signoff_id, version").Find(&revisions).Error; err != nil {
		return Page[model.ResultSignoff]{}, err
	}
	bySignoff := make(map[uint][]model.ResultSignoffRevision)
	for _, revision := range revisions {
		bySignoff[revision.ResultSignoffID] = append(bySignoff[revision.ResultSignoffID], revision)
	}
	if err := r.hydrate(ctx, page.Items, ids, bySignoff); err != nil {
		return Page[model.ResultSignoff]{}, err
	}
	return page, nil
}

func (r *resultSignoffRepository) Get(ctx context.Context, id uint) (model.ResultSignoff, error) {
	items := make([]model.ResultSignoff, 0, 1)
	err := r.db.WithContext(ctx).Preload("Revisions", func(db *gorm.DB) *gorm.DB {
		return db.Order("version")
	}).Limit(1).Find(&items, id).Error
	if err != nil {
		return model.ResultSignoff{}, err
	}
	if len(items) == 0 {
		return model.ResultSignoff{}, gorm.ErrRecordNotFound
	}
	if err := r.hydrate(ctx, items, []uint{id},
		map[uint][]model.ResultSignoffRevision{id: items[0].Revisions}); err != nil {
		return model.ResultSignoff{}, err
	}
	return items[0], nil
}

// hydrate attaches correction reviews and correction/original code links so the UI can
// render review status, the original result and the linked draft from the list payload.
func (r *resultSignoffRepository) hydrate(ctx context.Context, items []model.ResultSignoff, ids []uint, revisions map[uint][]model.ResultSignoffRevision) error {
	var corrections []model.SignoffCorrection
	if err := r.db.WithContext(ctx).Where("result_signoff_id IN ?", ids).
		Order("id").Find(&corrections).Error; err != nil {
		return err
	}
	latestBySignoff := make(map[uint]model.SignoffCorrection)
	for _, correction := range corrections {
		latestBySignoff[correction.ResultSignoffID] = correction
	}

	linkedIDSet := make(map[uint]struct{})
	for index := range items {
		items[index].Revisions = revisions[items[index].ID]
		latest, hasLatest := latestBySignoff[items[index].ID]
		if hasLatest {
			copyLatest := latest
			items[index].LatestCorrection = &copyLatest
			if latest.Status == model.SignoffCorrectionStatusOpen {
				copyOpen := latest
				items[index].OpenCorrection = &copyOpen
			}
			if latest.DraftSignoffID != nil {
				linkedIDSet[*latest.DraftSignoffID] = struct{}{}
			}
		}
		if items[index].CorrectionOfID != nil {
			linkedIDSet[*items[index].CorrectionOfID] = struct{}{}
		}
	}
	if len(linkedIDSet) > 0 {
		linkedIDs := make([]uint, 0, len(linkedIDSet))
		for id := range linkedIDSet {
			linkedIDs = append(linkedIDs, id)
		}
		var links []model.ResultSignoff
		if err := r.db.WithContext(ctx).Select("id", "code").Where("id IN ?", linkedIDs).Find(&links).Error; err != nil {
			return err
		}
		codeByID := make(map[uint]string, len(links))
		for _, link := range links {
			codeByID[link.ID] = link.Code
		}
		for index := range items {
			if items[index].CorrectionOfID != nil {
				items[index].OriginalCode = codeByID[*items[index].CorrectionOfID]
			}
			if latest := items[index].LatestCorrection; latest != nil && latest.DraftSignoffID != nil {
				items[index].CorrectionCode = codeByID[*latest.DraftSignoffID]
			}
		}
	}
	return nil
}

func (r *resultSignoffRepository) Transaction(ctx context.Context, work func(tx *gorm.DB) error) error {
	return r.db.WithContext(ctx).Transaction(work)
}

func (r *resultSignoffRepository) CreateVersion(ctx context.Context, item *model.ResultSignoff, actor, requestID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.CreateVersionTx(tx, item, actor, requestID, "create", "signoff drafted")
	})
}

func (r *resultSignoffRepository) CreateVersionTx(tx *gorm.DB, item *model.ResultSignoff, actor, requestID, action, reason string) error {
	if err := tx.Omit("Revisions", "Corrections").Create(item).Error; err != nil {
		return err
	}
	revision := model.ResultSignoffRevision{
		ResultSignoffID: item.ID, Version: item.Version, Status: item.Status,
		Evidence: item.Evidence, Actor: actor, RequestID: requestID, Action: action,
		Reason: reason, CreatedAt: item.CreatedAt,
	}
	if err := tx.Create(&revision).Error; err != nil {
		return err
	}
	return appendAudit(tx, actor, requestID, action, "ResultSignoff", item.ID, "", item.Status, reason)
}

func (r *resultSignoffRepository) UpdateVersion(ctx context.Context, id, expectedVersion uint, item *model.ResultSignoff, actor, requestID, action, before, reason string, supersedeID uint) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.updateVersionTx(tx, id, expectedVersion, item, actor, requestID, action, before, reason, supersedeID)
	})
}

func (r *resultSignoffRepository) updateVersionTx(tx *gorm.DB, id, expectedVersion uint, item *model.ResultSignoff, actor, requestID, action, before, reason string, supersedeID uint) error {
	item.Revisions = nil
	item.Corrections = nil
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
	if err := appendAudit(tx, actor, requestID, action, "ResultSignoff", id, before, item.Status, reason); err != nil {
		return err
	}
	if supersedeID != 0 && supersedeID != id {
		result := tx.Model(&model.ResultSignoff{}).Where("id = ? AND status = ?", supersedeID, model.SignoffStateSigned).
			Update("superseded", true)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrVersionConflict
		}
		if err := appendAudit(tx, actor, requestID, "supersede", "ResultSignoff", supersedeID,
			model.SignoffStateSigned, model.SignoffStateSigned, "signed result superseded by signed correction"); err != nil {
			return err
		}
	}
	return nil
}

func (r *resultSignoffRepository) MarkSupersededTx(tx *gorm.DB, originalID uint, actor, requestID, newCode string) error {
	result := tx.Model(&model.ResultSignoff{}).Where("id = ? AND status = ? AND superseded = ?",
		originalID, model.SignoffStateSigned, false).Update("superseded", true)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrVersionConflict
	}
	return appendAudit(tx, actor, requestID, "supersede", "ResultSignoff", originalID,
		model.SignoffStateSigned, model.SignoffStateSigned, "original signed result superseded by correction "+newCode)
}

func (r *resultSignoffRepository) Delete(ctx context.Context, id uint) error {
	return r.store.Delete(ctx, id)
}
func (r *resultSignoffRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}

var activeOpenSlot = "active"

func (r *resultSignoffRepository) CreateCorrection(ctx context.Context, correction *model.SignoffCorrection) error {
	correction.Status = model.SignoffCorrectionStatusOpen
	correction.OpenSlot = &activeOpenSlot
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(correction).Error; err != nil {
			if isDuplicateKeyError(err) {
				return ErrCorrectionConflict
			}
			return err
		}
		return appendAudit(tx, correction.RequestedBy, correction.RequestID, "correction_open",
			"SignoffCorrection", correction.ID, "", model.SignoffCorrectionStatusOpen,
			"post-signing review opened for signed result "+strings.TrimSpace(correction.Reason))
	})
}

func (r *resultSignoffRepository) GetCorrection(ctx context.Context, id uint) (model.SignoffCorrection, error) {
	var correction model.SignoffCorrection
	err := r.db.WithContext(ctx).First(&correction, id).Error
	return correction, err
}

func (r *resultSignoffRepository) FindOpenCorrection(ctx context.Context, signoffID uint) (model.SignoffCorrection, error) {
	var correction model.SignoffCorrection
	err := r.db.WithContext(ctx).
		Where("result_signoff_id = ? AND status = ?", signoffID, model.SignoffCorrectionStatusOpen).
		Order("id").First(&correction).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return correction, nil
	}
	return correction, err
}

// OpenCorrectionDraftID returns the linked draft id for the latest approved correction,
// provided that draft has not yet reached a terminal state (i.e. a correction is in flight).
func (r *resultSignoffRepository) OpenCorrectionDraftID(ctx context.Context, signoffID uint) (uint, error) {
	var correction model.SignoffCorrection
	err := r.db.WithContext(ctx).
		Where("result_signoff_id = ? AND status = ? AND draft_signoff_id IS NOT NULL", signoffID, model.SignoffCorrectionStatusApproved).
		Order("id DESC").First(&correction).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if correction.DraftSignoffID == nil {
		return 0, nil
	}
	var draft model.ResultSignoff
	if err := r.db.WithContext(ctx).Select("id", "status").First(&draft, *correction.DraftSignoffID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	if draft.Status == model.SignoffStateSigned || draft.Status == model.SignoffStateRejected {
		return 0, nil
	}
	return *correction.DraftSignoffID, nil
}

func (r *resultSignoffRepository) DecideCorrectionTx(tx *gorm.DB, correctionID uint, status, decidedBy, decisionNote, actor, requestID string) error {
	now := modelNow()
	result := tx.Model(&model.SignoffCorrection{}).
		Where("id = ? AND status = ?", correctionID, model.SignoffCorrectionStatusOpen).
		Updates(map[string]any{
			"status": status, "decided_by": decidedBy, "decision_note": strings.TrimSpace(decisionNote),
			"open_slot": nil, "decided_at": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrCorrectionNotOpen
	}
	detail := "post-signing review " + status
	if strings.TrimSpace(decisionNote) != "" {
		detail = detail + ": " + strings.TrimSpace(decisionNote)
	}
	return appendAudit(tx, actor, requestID, "correction_"+status,
		"SignoffCorrection", correctionID, model.SignoffCorrectionStatusOpen, status, detail)
}

func (r *resultSignoffRepository) LinkCorrectionDraftTx(tx *gorm.DB, correctionID, draftID uint) error {
	result := tx.Model(&model.SignoffCorrection{}).
		Where("id = ? AND status = ?", correctionID, model.SignoffCorrectionStatusApproved).
		Update("draft_signoff_id", draftID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrCorrectionNotOpen
	}
	return nil
}
