package dto

import "time"

// CreateResultSignoff is the public write contract for 结果签发. Status is deliberately
// omitted so callers cannot bypass the service state machine.
type CreateResultSignoff struct {
	Code        string    `json:"code" binding:"required,min=2,max=64"`
	Name        string    `json:"name" binding:"required,min=2,max=160"`
	Description string    `json:"description" binding:"max=1000"`
	Facility    string    `json:"facility" binding:"required,max=120"`
	Owner       string    `json:"owner" binding:"required,max=120"`
	Category    string    `json:"category" binding:"required,max=80"`
	RiskLevel   string    `json:"riskLevel" binding:"required,oneof=low medium high critical"`
	MetricValue float64   `json:"metricValue"`
	MetricUnit  string    `json:"metricUnit" binding:"max=24"`
	EffectiveAt time.Time `json:"effectiveAt" binding:"required"`
	Evidence    string    `json:"evidence" binding:"max=2000"`
	RelatedCode string    `json:"relatedCode" binding:"max=64"`
}

type UpdateResultSignoff struct {
	ExpectedVersion uint      `json:"expectedVersion" binding:"required"`
	Name            string    `json:"name" binding:"required,min=2,max=160"`
	Description     string    `json:"description" binding:"max=1000"`
	Facility        string    `json:"facility" binding:"required,max=120"`
	Owner           string    `json:"owner" binding:"required,max=120"`
	Category        string    `json:"category" binding:"required,max=80"`
	RiskLevel       string    `json:"riskLevel" binding:"required,oneof=low medium high critical"`
	MetricValue     float64   `json:"metricValue"`
	MetricUnit      string    `json:"metricUnit" binding:"max=24"`
	EffectiveAt     time.Time `json:"effectiveAt" binding:"required"`
	Evidence        string    `json:"evidence" binding:"max=2000"`
	RelatedCode     string    `json:"relatedCode" binding:"max=64"`
}

// OpenSignoffReview starts a correction review for a signed result. Both the
// reason and supporting evidence are mandatory.
type OpenSignoffReview struct {
	Reason   string `json:"reason" binding:"required,min=3,max=500"`
	Evidence string `json:"evidence" binding:"required,min=3,max=2000"`
}

// DecideSignoffReview resolves a correction review. Decision "upheld" copies
// the signed result into a linked draft; "rejected" only closes the review.
type DecideSignoffReview struct {
	ReviewID        uint   `json:"reviewId" binding:"required"`
	Decision        string `json:"decision" binding:"required,oneof=upheld rejected"`
	Reason          string `json:"reason" binding:"required,min=3,max=500"`
	ExpectedVersion uint   `json:"expectedVersion" binding:"required"`
}
