package compaction

import (
	"context"

	"github.com/google/uuid"
)

// ThresholdDeduplicator is a non-LLM CandidateDeduplicator that keeps or
// archives candidates based on confidence and extraction score thresholds.
// It does not detect semantic duplicates (that requires an LLM), but it
// allows the sleep reflection pipeline to run and apply score updates,
// confidence decay, and archival of low-quality memories.
//
// Candidates are evaluated as follows:
//   - confidence < 0.30 → archive
//   - confidence >= 0.75 → keep, ExtractionScore=85 (high quality)
//   - confidence >= 0.50 → keep, ExtractionScore=60 (medium quality)
//   - otherwise → keep, ExtractionScore=nil (no score update)
type ThresholdDeduplicator struct {
	HighConfidenceThreshold   float64
	MediumConfidenceThreshold float64
	ArchiveThreshold          float64
	HighExtractionScore       int
	MediumExtractionScore     int
}

// DefaultThresholdDeduplicator returns a ThresholdDeduplicator with sensible defaults.
func DefaultThresholdDeduplicator() *ThresholdDeduplicator {
	return &ThresholdDeduplicator{
		HighConfidenceThreshold:   0.75,
		MediumConfidenceThreshold: 0.50,
		ArchiveThreshold:          0.30,
		HighExtractionScore:       85,
		MediumExtractionScore:     60,
	}
}

// ReviewCandidateBatch implements CandidateDeduplicator using confidence thresholds.
func (d *ThresholdDeduplicator) ReviewCandidateBatch(_ context.Context, _ uuid.UUID, batch []CandidateMemory) ([]CandidateReview, error) {
	reviews := make([]CandidateReview, 0, len(batch))
	for _, candidate := range batch {
		review := CandidateReview{
			MemoryID: candidate.ID,
		}

		if candidate.Confidence < d.ArchiveThreshold {
			review.Action = CandidateReviewArchive
			reviews = append(reviews, review)
			continue
		}

		review.Action = CandidateReviewKeep
		if candidate.Confidence >= d.HighConfidenceThreshold {
			score := d.HighExtractionScore
			review.ExtractionScore = &score
		} else if candidate.Confidence >= d.MediumConfidenceThreshold {
			score := d.MediumExtractionScore
			review.ExtractionScore = &score
		}
		// Below medium threshold: keep with no extraction score update

		reviews = append(reviews, review)
	}
	return reviews, nil
}
