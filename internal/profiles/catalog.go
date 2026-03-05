package profiles

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RosterEntry is the parsed content of a profile's roster_entry.json.
type RosterEntry struct {
	RoleID         string   `json:"role_id"`
	DisplayName    string   `json:"display_name"`
	Pronouns       string   `json:"pronouns"`
	RoleName       string   `json:"role_name"`
	Emoji          string   `json:"emoji"`
	RoleType       string   `json:"role_type"`
	Category       string   `json:"category"`
	Subcategory    string   `json:"subcategory"`
	Tagline        string   `json:"tagline"`
	DifficultyTier string   `json:"difficulty_tier"`
	SoloOrTeam     string   `json:"solo_or_team"`
	PairsWellWith  []string `json:"pairs_well_with"`
	Pros           []string `json:"pros"`
	Cons           []string `json:"cons"`
}

// ProfileDetail includes the full identity content alongside the roster entry.
type ProfileDetail struct {
	RosterEntry
	IdentitySummary string
	Soul            string
}

// Catalog holds all loaded agent profiles in memory.
type Catalog struct {
	entries    []RosterEntry
	byRoleID   map[string]int // index into entries
	details    map[string]*ProfileDetail
	categories []string
}

// LoadCatalog reads all agent-profiles from dir. Each subdirectory with a
// roster_entry.json is loaded. Directories without roster_entry.json are skipped.
func LoadCatalog(dir string) (*Catalog, error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	c := &Catalog{
		byRoleID: make(map[string]int),
		details:  make(map[string]*ProfileDetail),
	}
	catSet := make(map[string]struct{})

	for _, de := range dirEntries {
		if !de.IsDir() {
			continue
		}
		profileDir := filepath.Join(dir, de.Name())
		rosterPath := filepath.Join(profileDir, "roster_entry.json")

		rosterData, err := os.ReadFile(rosterPath)
		if err != nil {
			continue // skip dirs without roster_entry.json
		}

		var entry RosterEntry
		if err := json.Unmarshal(rosterData, &entry); err != nil {
			continue
		}
		if entry.RoleID == "" {
			entry.RoleID = de.Name()
		}

		idx := len(c.entries)
		c.entries = append(c.entries, entry)
		c.byRoleID[entry.RoleID] = idx

		if entry.Category != "" {
			catSet[entry.Category] = struct{}{}
		}

		detail := &ProfileDetail{RosterEntry: entry}
		if data, err := os.ReadFile(filepath.Join(profileDir, "IDENTITY_SUMMARY.md")); err == nil {
			detail.IdentitySummary = string(data)
		}
		if data, err := os.ReadFile(filepath.Join(profileDir, "SOUL.md")); err == nil {
			detail.Soul = string(data)
		}
		c.details[entry.RoleID] = detail
	}

	c.categories = make([]string, 0, len(catSet))
	for cat := range catSet {
		c.categories = append(c.categories, cat)
	}
	sort.Strings(c.categories)

	return c, nil
}

// Search filters profiles by category (exact), roleType (exact), and query
// (case-insensitive substring on role_name, tagline, display_name).
func (c *Catalog) Search(category, roleType, query string, limit int) []RosterEntry {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	results := make([]RosterEntry, 0, limit)

	for _, entry := range c.entries {
		if category != "" && !strings.EqualFold(entry.Category, category) {
			continue
		}
		if roleType != "" && !strings.EqualFold(entry.RoleType, roleType) {
			continue
		}
		if lowerQuery != "" {
			if !strings.Contains(strings.ToLower(entry.RoleName), lowerQuery) &&
				!strings.Contains(strings.ToLower(entry.Tagline), lowerQuery) &&
				!strings.Contains(strings.ToLower(entry.DisplayName), lowerQuery) {
				continue
			}
		}
		results = append(results, entry)
		if len(results) >= limit {
			break
		}
	}
	return results
}

// GetByRoleID returns the full profile detail for a given role_id.
func (c *Catalog) GetByRoleID(roleID string) (*ProfileDetail, bool) {
	detail, ok := c.details[roleID]
	return detail, ok
}

// Categories returns sorted unique categories across all profiles.
func (c *Catalog) Categories() []string {
	return c.categories
}
