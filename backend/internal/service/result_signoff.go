package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/veterinary-lab-result-review/backend/internal/constants"
	"github.com/blueship581/veterinary-lab-result-review/backend/internal/dto"
	"github.com/blueship581/veterinary-lab-result-review/backend/internal/model"
	"github.com/blueship581/veterinary-lab-result-review/backend/internal/repository"
	"gorm.io/gorm"
)

type ResultSignoffService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.ResultSignoff], error)
	Get(context.Context, uint) (model.ResultSignoff, error)
	Create(context.Context, dto.CreateResultSignoff, string, string) (model.ResultSignoff, error)
	Update(context.Context, uint, dto.UpdateResultSignoff, string, string) (model.ResultSignoff, error)
	Transition(context.Context, uint, dto.TransitionRequest, string, string, string) (model.ResultSignoff, error)
	Delete(context.Context, uint, string, string) error
	StatusCounts(context.Context) (map[string]int64, error)
	OpenCorrection(context.Context, uint, dto.OpenCorrectionRequest, string, string, string) (model.SignoffCorrection, error)
	DecideCorrection(context.Context, uint, uint, dto.CorrectionDecisionRequest, string, string, string) (model.SignoffCorrection, error)
	GetCorrection(context.Context, uint) (model.SignoffCorrection, error)
}

type resultSignoffService struct {
	repository repository.ResultSignoffRepository
	security   SecurityService
}

func NewResultSignoffService(repo repository.ResultSignoffRepository, security SecurityService) ResultSignoffService {
	return &resultSignoffService{repository: repo, security: security}
}

func (s *resultSignoffService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.ResultSignoff], error) {
	return s.repository.List(ctx, query)
}

func (s *resultSignoffService) Get(ctx context.Context, id uint) (model.ResultSignoff, error) {
	return s.repository.Get(ctx, id)
}

func (s *resultSignoffService) Create(ctx context.Context, input dto.CreateResultSignoff, actor, requestID string) (model.ResultSignoff, error) {
	if err := validateResultSignoffBusinessFields(input.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.ResultSignoff{}, err
	}
	item := model.ResultSignoff{
		BaseModel: model.BaseModel{
			Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
			Status: model.ResultSignoffInitialStatus, Version: 1, Description: strings.TrimSpace(input.Description),
		},
		Facility: strings.TrimSpace(input.Facility), Owner: strings.TrimSpace(input.Owner),
		Category: strings.TrimSpace(input.Category), RiskLevel: input.RiskLevel,
		MetricValue: input.MetricValue, MetricUnit: strings.TrimSpace(input.MetricUnit),
		EffectiveAt: input.EffectiveAt.UTC(), Evidence: strings.TrimSpace(input.Evidence),
		RelatedCode: strings.ToUpper(strings.TrimSpace(input.RelatedCode)), PreparedBy: actor,
	}
	if err := s.repository.CreateVersion(ctx, &item, actor, requestID); err != nil {
		return model.ResultSignoff{}, fmt.Errorf("create 结果签发: %w", err)
	}
	return s.repository.Get(ctx, item.ID)
}

func (s *resultSignoffService) Update(ctx context.Context, id uint, input dto.UpdateResultSignoff, actor, requestID string) (model.ResultSignoff, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ResultSignoff{}, err
	}
	if current.Status != model.ResultSignoffInitialStatus {
		return model.ResultSignoff{}, ErrLocked
	}
	if actor != current.PreparedBy {
		return model.ResultSignoff{}, ErrPreparationOwner
	}
	if err := validateResultSignoffBusinessFields(current.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.ResultSignoff{}, err
	}
	current.Name = strings.TrimSpace(input.Name)
	current.Description = strings.TrimSpace(input.Description)
	current.Facility = strings.TrimSpace(input.Facility)
	current.Owner = strings.TrimSpace(input.Owner)
	current.Category = strings.TrimSpace(input.Category)
	current.RiskLevel = input.RiskLevel
	current.MetricValue = input.MetricValue
	current.MetricUnit = strings.TrimSpace(input.MetricUnit)
	current.EffectiveAt = input.EffectiveAt.UTC()
	current.Evidence = strings.TrimSpace(input.Evidence)
	current.RelatedCode = strings.ToUpper(strings.TrimSpace(input.RelatedCode))
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.UpdateVersion(ctx, id, input.ExpectedVersion, &current, actor, requestID, "update", current.Status, "draft signoff fields updated", 0); err != nil {
		return model.ResultSignoff{}, fmt.Errorf("update 结果签发: %w", err)
	}
	return s.repository.Get(ctx, id)
}

func (s *resultSignoffService) Transition(ctx context.Context, id uint, input dto.TransitionRequest, actor, role, requestID string) (model.ResultSignoff, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ResultSignoff{}, err
	}
	target := strings.TrimSpace(input.Status)
	if !constants.CanTransition(constants.ResultSignoffTransitions, current.Status, target) {
		return model.ResultSignoff{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Status, target)
	}
	if !isSignoffOperatorRole(role) {
		return model.ResultSignoff{}, ErrForbidden
	}
	if target == "peer_review" {
		if actor != current.PreparedBy {
			return model.ResultSignoff{}, ErrPreparationOwner
		}
		current.ReviewedBy = ""
		current.ReviewReason = ""
	}
	if target == "signed" || target == "rejected" {
		if !isSignoffReviewerRole(role) {
			return model.ResultSignoff{}, ErrReviewRequired
		}
		if actor == current.PreparedBy {
			return model.ResultSignoff{}, ErrSeparationOfDuty
		}
		current.ReviewedBy = actor
		current.ReviewReason = strings.TrimSpace(input.Reason)
	}
	before := current.Status
	current.Status = target
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	var supersedeID uint
	// When a correction draft becomes the new signed result, retire the original
	// signed record in the same atomic version update.
	if target == "signed" && current.CorrectionOfID != nil {
		supersedeID = *current.CorrectionOfID
	}
	if err := s.repository.UpdateVersion(ctx, id, input.ExpectedVersion, &current, actor, requestID, "transition", before, strings.TrimSpace(input.Reason), supersedeID); err != nil {
		return model.ResultSignoff{}, fmt.Errorf("transition 结果签发: %w", err)
	}
	return s.repository.Get(ctx, id)
}

func (s *resultSignoffService) Delete(ctx context.Context, id uint, actor, requestID string) error {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.Status != model.ResultSignoffInitialStatus {
		return ErrLocked
	}
	if err := s.repository.Delete(ctx, id); err != nil {
		return err
	}
	return s.security.Audit(ctx, actor, requestID, "delete", "ResultSignoff", id, current.Status, "deleted", "soft deleted 结果签发")
}

func isSignoffOperatorRole(role string) bool {
	return role == model.RoleOperator || role == model.RoleReviewer || role == model.RoleAdmin
}

func isSignoffReviewerRole(role string) bool {
	return role == model.RoleReviewer || role == model.RoleAdmin
}

// OpenCorrection starts a post-signing review for a signed result. reviewer/admin may
// open one, the reviewer must differ from the original signer, reason and evidence are
// mandatory, and only a single open review is kept even under duplicate/concurrent calls.
func (s *resultSignoffService) OpenCorrection(ctx context.Context, signoffID uint, input dto.OpenCorrectionRequest, actor, role, requestID string) (model.SignoffCorrection, error) {
	if !isSignoffReviewerRole(role) {
		return model.SignoffCorrection{}, ErrForbidden
	}
	reason := strings.TrimSpace(input.Reason)
	evidence := strings.TrimSpace(input.Evidence)
	if len(reason) < 3 || len(evidence) < 3 {
		return model.SignoffCorrection{}, ErrInvalidInput
	}
	current, err := s.repository.Get(ctx, signoffID)
	if err != nil {
		return model.SignoffCorrection{}, err
	}
	if current.Status != "signed" {
		return model.SignoffCorrection{}, fmt.Errorf("%w: only signed results accept correction reviews", ErrInvalidTransition)
	}
	if actor == current.ReviewedBy || actor == current.PreparedBy {
		return model.SignoffCorrection{}, ErrSeparationOfDuty
	}
	// Friendly guard before the unique index enforces single-flight.
	if open, err := s.repository.FindOpenCorrection(ctx, signoffID); err != nil {
		return model.SignoffCorrection{}, err
	} else if open.ID != 0 {
		return model.SignoffCorrection{}, ErrCorrectionConflict
	}
	if current.Superseded {
		return model.SignoffCorrection{}, ErrSuperseded
	}
	inFlightDraft, err := s.repository.OpenCorrectionDraftID(ctx, signoffID)
	if err != nil {
		return model.SignoffCorrection{}, err
	}
	if inFlightDraft != 0 {
		return model.SignoffCorrection{}, ErrCorrectionConflict
	}
	correction := model.SignoffCorrection{
		ResultSignoffID: signoffID, Reason: reason, Evidence: evidence,
		RequestedBy: actor, RequestID: requestID, CreatedAt: time.Now().UTC(),
	}
	if err := s.repository.CreateCorrection(ctx, &correction); err != nil {
		if errors.Is(err, repository.ErrCorrectionConflict) {
			return model.SignoffCorrection{}, ErrCorrectionConflict
		}
		return model.SignoffCorrection{}, fmt.Errorf("open correction review: %w", err)
	}
	return correction, nil
}

// DecideCorrection closes an open review. Approving keeps the original signed result
// untouched and creates a linked draft prepared by the approver; rejecting only closes
// the review. Either branch is atomic and rolls back on any failure.
func (s *resultSignoffService) DecideCorrection(ctx context.Context, signoffID, correctionID uint, input dto.CorrectionDecisionRequest, actor, role, requestID string) (model.SignoffCorrection, error) {
	if !isSignoffReviewerRole(role) {
		return model.SignoffCorrection{}, ErrForbidden
	}
	current, err := s.repository.Get(ctx, signoffID)
	if err != nil {
		return model.SignoffCorrection{}, err
	}
	correction, err := s.repository.GetCorrection(ctx, correctionID)
	if err != nil {
		return model.SignoffCorrection{}, err
	}
	if correction.ResultSignoffID != signoffID || correction.Status != model.SignoffCorrectionStatusOpen {
		return model.SignoffCorrection{}, ErrCorrectionNotOpen
	}
	if actor == current.ReviewedBy || actor == current.PreparedBy {
		return model.SignoffCorrection{}, ErrSeparationOfDuty
	}
	if input.Approve {
		err = s.repository.Transaction(ctx, func(tx *gorm.DB) error {
			return s.approveCorrection(tx, current, correction, actor, requestID, strings.TrimSpace(input.DecisionNote))
		})
	} else {
		err = s.repository.Transaction(ctx, func(tx *gorm.DB) error {
			return s.repository.DecideCorrectionTx(tx, correction.ID, model.SignoffCorrectionStatusRejected, actor, input.DecisionNote, actor, requestID)
		})
	}
	if err != nil {
		if errors.Is(err, repository.ErrCorrectionConflict) {
			return model.SignoffCorrection{}, ErrCorrectionConflict
		}
		if errors.Is(err, repository.ErrCorrectionNotOpen) {
			return model.SignoffCorrection{}, ErrCorrectionNotOpen
		}
		return model.SignoffCorrection{}, fmt.Errorf("decide correction review: %w", err)
	}
	return s.repository.GetCorrection(ctx, correctionID)
}

func (s *resultSignoffService) approveCorrection(tx *gorm.DB, original model.ResultSignoff, correction model.SignoffCorrection, actor, requestID, decisionNote string) error {
	if err := s.repository.DecideCorrectionTx(tx, correction.ID, model.SignoffCorrectionStatusApproved, actor, decisionNote, actor, requestID); err != nil {
		return err
	}
	draft := buildCorrectionDraft(original, correction.ID, actor)
	if err := s.repository.CreateVersionTx(tx, &draft, actor, requestID, "correction_draft",
		"correction approved; linked draft created from signed result"); err != nil {
		return err
	}
	return s.repository.LinkCorrectionDraftTx(tx, correction.ID, draft.ID)
}

func (s *resultSignoffService) GetCorrection(ctx context.Context, correctionID uint) (model.SignoffCorrection, error) {
	return s.repository.GetCorrection(ctx, correctionID)
}

// buildCorrectionDraft copies the signed result into a fresh v1 draft owned by the
// approving reviewer and linked back to the signed record it corrects.
func buildCorrectionDraft(original model.ResultSignoff, correctionID uint, actor string) model.ResultSignoff {
	now := time.Now().UTC()
	return model.ResultSignoff{
		BaseModel: model.BaseModel{
			Code:        fmt.Sprintf("%s-RC%d", truncateCode(original.Code, 48), correctionID),
			Name:        original.Name + "（复核更正稿）",
			Status:      model.ResultSignoffInitialStatus,
			Version:     1,
			Description: strings.TrimSpace(original.Description),
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		Facility: original.Facility, Owner: original.Owner, Category: original.Category,
		RiskLevel: original.RiskLevel, MetricValue: original.MetricValue, MetricUnit: original.MetricUnit,
		EffectiveAt: original.EffectiveAt, Evidence: original.Evidence, RelatedCode: original.Code,
		PreparedBy: actor, CorrectionOfID: &original.ID,
	}
}

func truncateCode(code string, limit int) string {
	if len(code) <= limit {
		return code
	}
	return code[:limit]
}

func (s *resultSignoffService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	return s.repository.CountByStatus(ctx)
}

func validateResultSignoffBusinessFields(code, name, facility, owner string) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(facility) == "" || strings.TrimSpace(owner) == "" {
		return ErrInvalidInput
	}
	return nil
}
