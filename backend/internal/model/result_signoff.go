package model

import "time"

// ResultSignoff models 结果签发 as an independently versioned aggregate. The fields
// cover ownership, operational context, evidence and measured risk so later
// changes naturally span persistence, service and UI layers.
type ResultSignoff struct {
	BaseModel
	Facility     string    `json:"facility" gorm:"size:120;index"`
	Owner        string    `json:"owner" gorm:"size:120;index"`
	Category     string    `json:"category" gorm:"size:80;index"`
	RiskLevel    string    `json:"riskLevel" gorm:"size:32;index"`
	MetricValue  float64   `json:"metricValue"`
	MetricUnit   string    `json:"metricUnit" gorm:"size:24"`
	EffectiveAt  time.Time `json:"effectiveAt"`
	Evidence     string    `json:"evidence" gorm:"size:2000"`
	RelatedCode  string    `json:"relatedCode" gorm:"size:64;index"`
	PreparedBy   string    `json:"preparedBy" gorm:"size:80;index"`
	ReviewedBy   string    `json:"reviewedBy" gorm:"size:80;index"`
	ReviewReason string    `json:"reviewReason" gorm:"size:500"`
	// CorrectionOfID links a correction draft back to the signed record it revises.
	CorrectionOfID *uint `json:"correctionOfId,omitempty" gorm:"index"`
	// Superseded marks a signed record that was replaced by a signed correction draft.
	Superseded  bool                    `json:"superseded" gorm:"not null;default:false;index"`
	Revisions   []ResultSignoffRevision `json:"revisions,omitempty" gorm:"foreignKey:ResultSignoffID"`
	Corrections []SignoffCorrection     `json:"corrections,omitempty" gorm:"foreignKey:ResultSignoffID"`

	// Read-only hydration fields populated by the repository; never persisted.
	OpenCorrection *SignoffCorrection `json:"openCorrection,omitempty" gorm:"-"`
	// LatestCorrection holds the most recent review (including approved/rejected) so the
	// UI can show review status, the linked new draft and the superseding version.
	LatestCorrection *SignoffCorrection `json:"latestCorrection,omitempty" gorm:"-"`
	OriginalCode     string             `json:"originalCode,omitempty" gorm:"-"`
	CorrectionCode   string             `json:"correctionCode,omitempty" gorm:"-"`
}

func (item *ResultSignoff) GetBase() *BaseModel { return &item.BaseModel }

func (item ResultSignoff) TableName() string { return "result_signoffs" }

var ResultSignoffInitialStatus = "draft"

const (
	SignoffStateDraft      = "draft"
	SignoffStatePeerReview = "peer_review"
	SignoffStateSigned     = "signed"
	SignoffStateRejected   = "rejected"
)

// SignoffCorrectionStatus enumerates the post-signing review (复核更正) lifecycle.
const (
	SignoffCorrectionStatusOpen     = "open"
	SignoffCorrectionStatusApproved = "approved"
	SignoffCorrectionStatusRejected = "rejected"
)

var AllSignoffCorrectionStatus = []string{
	SignoffCorrectionStatusOpen, SignoffCorrectionStatusApproved, SignoffCorrectionStatusRejected,
}

// SignoffCorrection is a review opened against an already signed result. Exactly one
// open correction may exist per signoff: OpenSlot is "active" while open and NULL once a
// decision is taken, which lets a unique index enforce single-flight across MySQL/SQLite.
type SignoffCorrection struct {
	ID              uint       `json:"id" gorm:"primaryKey"`
	ResultSignoffID uint       `json:"resultSignoffId" gorm:"not null;index"`
	Status          string     `json:"status" gorm:"size:32;not null;index"`
	Reason          string     `json:"reason" gorm:"size:500;not null"`
	Evidence        string     `json:"evidence" gorm:"size:2000;not null"`
	RequestedBy     string     `json:"requestedBy" gorm:"size:80;not null;index"`
	DecidedBy       string     `json:"decidedBy,omitempty" gorm:"size:80;index"`
	DecisionNote    string     `json:"decisionNote,omitempty" gorm:"size:500"`
	DraftSignoffID  *uint      `json:"draftSignoffId,omitempty" gorm:"index"`
	RequestID       string     `json:"requestId" gorm:"size:64;not null;index"`
	OpenSlot        *string    `json:"-" gorm:"size:16;uniqueIndex:idx_signoff_open_correction"`
	CreatedAt       time.Time  `json:"createdAt" gorm:"index"`
	DecidedAt       *time.Time `json:"decidedAt,omitempty"`
}

func (SignoffCorrection) TableName() string { return "signoff_corrections" }

// ResultSignoffRevision is append-only evidence for every signoff version.
type ResultSignoffRevision struct {
	ID              uint      `json:"id" gorm:"primaryKey"`
	ResultSignoffID uint      `json:"resultSignoffId" gorm:"not null;index;uniqueIndex:idx_signoff_revision_version,priority:1"`
	Version         uint      `json:"version" gorm:"not null;uniqueIndex:idx_signoff_revision_version,priority:2"`
	Status          string    `json:"status" gorm:"size:40;not null"`
	Evidence        string    `json:"evidence" gorm:"size:2000"`
	Actor           string    `json:"actor" gorm:"size:80;not null;index"`
	RequestID       string    `json:"requestId" gorm:"size:64;not null;index"`
	Action          string    `json:"action" gorm:"size:40;not null"`
	Reason          string    `json:"reason" gorm:"size:500"`
	CreatedAt       time.Time `json:"createdAt" gorm:"index"`
}
