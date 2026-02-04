// Package feed provides feed item processing, summarization, and ranking.
package feed

import (
	"sort"
	"time"
)

// DefaultSummaryBucket is the default time bucket size for summaries.
const DefaultSummaryBucket = time.Hour

// SummaryFilter defines filtering and bucketing options for summaries.
type SummaryFilter struct {
	ProjectID  string
	AgentID    string
	From       *time.Time
	To         *time.Time
	LastReadAt *time.Time
	BucketSize time.Duration
}

// Summary aggregates feed activity by type, agent, and time bucket.
type Summary struct {
	Total   int                      `json:"total"`
	Unread  int                      `json:"unread"`
	ByType  map[string]int           `json:"by_type,omitempty"`
	ByAgent map[string]*AgentSummary `json:"by_agent,omitempty"`
	ByTime  []TimeBucketSummary      `json:"by_time,omitempty"`
}

// AgentSummary aggregates activity for a specific agent.
type AgentSummary struct {
	AgentID string              `json:"agent_id"`
	Total   int                 `json:"total"`
	Unread  int                 `json:"unread"`
	ByType  map[string]int      `json:"by_type,omitempty"`
	ByTime  []TimeBucketSummary `json:"by_time,omitempty"`
}

// TimeBucketSummary aggregates activity within a time window.
type TimeBucketSummary struct {
	Start   time.Time      `json:"start"`
	End     time.Time      `json:"end"`
	Total   int            `json:"total"`
	ByType  map[string]int `json:"by_type,omitempty"`
	ByAgent map[string]int `json:"by_agent,omitempty"`
}

// ComputeSummary computes a summary for the given feed items and filter.
func ComputeSummary(items []*Item, filter SummaryFilter) Summary {
	bucketSize := filter.BucketSize
	if bucketSize <= 0 {
		bucketSize = DefaultSummaryBucket
	}

	summary := Summary{
		ByType:  make(map[string]int),
		ByAgent: make(map[string]*AgentSummary),
	}

	bucketMap := make(map[time.Time]*TimeBucketSummary)
	agentBuckets := make(map[string]map[time.Time]*TimeBucketSummary)

	for _, item := range items {
		if item == nil {
			continue
		}
		if !matchesSummaryFilter(item, filter) {
			continue
		}

		summary.Total++

		if filter.LastReadAt != nil && item.CreatedAt.After(*filter.LastReadAt) {
			summary.Unread++
		}

		if item.Type != "" {
			summary.ByType[item.Type]++
		}

		agentID := agentIDFromItem(item)
		if agentID != "" {
			agentSummary := summary.ByAgent[agentID]
			if agentSummary == nil {
				agentSummary = &AgentSummary{
					AgentID: agentID,
					ByType:  make(map[string]int),
				}
				summary.ByAgent[agentID] = agentSummary
			}

			agentSummary.Total++
			if filter.LastReadAt != nil && item.CreatedAt.After(*filter.LastReadAt) {
				agentSummary.Unread++
			}
			if item.Type != "" {
				agentSummary.ByType[item.Type]++
			}
		}

		bucketStart := item.CreatedAt.Truncate(bucketSize)
		bucket := bucketMap[bucketStart]
		if bucket == nil {
			bucket = &TimeBucketSummary{
				Start:   bucketStart,
				End:     bucketStart.Add(bucketSize),
				ByType:  make(map[string]int),
				ByAgent: make(map[string]int),
			}
			bucketMap[bucketStart] = bucket
		}
		bucket.Total++
		if item.Type != "" {
			bucket.ByType[item.Type]++
		}
		if agentID != "" {
			bucket.ByAgent[agentID]++
		}

		if agentID != "" {
			agentBucketMap := agentBuckets[agentID]
			if agentBucketMap == nil {
				agentBucketMap = make(map[time.Time]*TimeBucketSummary)
				agentBuckets[agentID] = agentBucketMap
			}
			agentBucket := agentBucketMap[bucketStart]
			if agentBucket == nil {
				agentBucket = &TimeBucketSummary{
					Start:  bucketStart,
					End:    bucketStart.Add(bucketSize),
					ByType: make(map[string]int),
				}
				agentBucketMap[bucketStart] = agentBucket
			}
			agentBucket.Total++
			if item.Type != "" {
				agentBucket.ByType[item.Type]++
			}
		}
	}

	summary.ByTime = sortedBuckets(bucketMap)

	for agentID, bucketMap := range agentBuckets {
		if agentSummary := summary.ByAgent[agentID]; agentSummary != nil {
			agentSummary.ByTime = sortedBuckets(bucketMap)
		}
	}

	return summary
}

func matchesSummaryFilter(item *Item, filter SummaryFilter) bool {
	if filter.ProjectID != "" {
		projectID := projectIDFromItem(item)
		if projectID == "" || projectID != filter.ProjectID {
			return false
		}
	}

	if filter.AgentID != "" {
		agentID := agentIDFromItem(item)
		if agentID == "" || agentID != filter.AgentID {
			return false
		}
	}

	if filter.From != nil && item.CreatedAt.Before(*filter.From) {
		return false
	}
	if filter.To != nil && item.CreatedAt.After(*filter.To) {
		return false
	}

	return true
}

func projectIDFromItem(item *Item) string {
	if item == nil {
		return ""
	}

	projectID := extractMetadataString(item.Metadata, "project_id")
	if projectID != "" {
		return projectID
	}
	projectID = extractMetadataString(item.Metadata, "project")
	if projectID != "" {
		return projectID
	}
	projectID = extractMetadataString(item.Metadata, "projectId")
	if projectID != "" {
		return projectID
	}
	projectID = extractMetadataString(item.Metadata, "repo")
	if projectID != "" {
		return projectID
	}

	return ""
}

func agentIDFromItem(item *Item) string {
	if item == nil {
		return ""
	}
	if item.AgentID != nil && *item.AgentID != "" {
		return *item.AgentID
	}

	agentID := extractMetadataString(item.Metadata, "agent_id")
	if agentID != "" {
		return agentID
	}
	agentID = extractMetadataString(item.Metadata, "agent")
	if agentID != "" {
		return agentID
	}
	agentID = extractMetadataString(item.Metadata, "agentId")
	if agentID != "" {
		return agentID
	}

	return ""
}

func sortedBuckets(bucketMap map[time.Time]*TimeBucketSummary) []TimeBucketSummary {
	if len(bucketMap) == 0 {
		return nil
	}

	starts := make([]time.Time, 0, len(bucketMap))
	for start := range bucketMap {
		starts = append(starts, start)
	}
	sort.Slice(starts, func(i, j int) bool {
		return starts[i].Before(starts[j])
	})

	buckets := make([]TimeBucketSummary, 0, len(starts))
	for _, start := range starts {
		bucket := bucketMap[start]
		if bucket == nil {
			continue
		}
		buckets = append(buckets, *bucket)
	}

	return buckets
}
