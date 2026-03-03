package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/validation"
)

const (
	defaultSkillsDir = "./skills/"
	systemActorType  = "system"
)

var (
	slugPattern                 = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	systemActorID               = uuid.MustParse("00000000-0000-0000-0000-000000000000")
	ErrInvalidSlug              = errors.New("invalid slug")
	ErrProjectScopedUnsupported = errors.New("project-scoped skills are not supported until task 016")
	ErrDisplayNameRequired      = errors.New("display_name is required")
	ErrDisplayNameInvalid       = errors.New("display_name contains HTML tags")
	ErrFilePathRequired         = errors.New("file_path is required")
	ErrCreatedByTypeInvalid     = errors.New("created_by_type must be human, agent, or system")
)

type Service interface {
	Create(ctx context.Context, orgID uuid.UUID, req CreateRequest) (*Skill, error)
	Update(ctx context.Context, skillID uuid.UUID, req UpdateRequest) (*Skill, error)
	Delete(ctx context.Context, skillID uuid.UUID) error
	Get(ctx context.Context, skillID uuid.UUID) (*Skill, error)
	List(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID) ([]*Skill, error)
	CheckConsistency(ctx context.Context, orgID uuid.UUID, skillsDir string) (*ConsistencyReport, error)
}

type Skill = repo.Skill

type CreateRequest struct {
	ProjectID     *uuid.UUID
	Slug          string
	DisplayName   string
	Description   string
	FilePath      string
	CreatedByType string
	CreatedByID   uuid.UUID
}

type UpdateRequest struct {
	DisplayName *string
	Description *string
	FilePath    *string
}

type ConsistencyReport struct {
	MissingFiles      []string
	UnregisteredFiles []string
	Mismatches        []string
}

type Repository interface {
	Create(ctx context.Context, skill repo.Skill) (repo.Skill, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.Skill, error)
	GetBySlug(ctx context.Context, organizationID uuid.UUID, projectID *uuid.UUID, slug string) (repo.Skill, error)
	ListByOrg(ctx context.Context, organizationID uuid.UUID, includeInactive bool) ([]repo.Skill, error)
	ListByProject(ctx context.Context, projectID uuid.UUID, includeInactive bool) ([]repo.Skill, error)
	Update(ctx context.Context, skill repo.Skill) (repo.Skill, error)
	SetActive(ctx context.Context, id uuid.UUID, active bool) (repo.Skill, error)
	BulkUpsertBySlug(ctx context.Context, skills []repo.Skill) ([]repo.Skill, error)
}

type service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return &service{repo: repo}
}

func (s *service) Create(ctx context.Context, orgID uuid.UUID, req CreateRequest) (*Skill, error) {
	if req.ProjectID != nil {
		return nil, ErrProjectScopedUnsupported
	}

	slug := strings.TrimSpace(req.Slug)
	if !slugPattern.MatchString(slug) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidSlug, req.Slug)
	}

	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		return nil, ErrDisplayNameRequired
	}
	if validation.HasHTMLTag(displayName) {
		return nil, ErrDisplayNameInvalid
	}

	filePath := strings.TrimSpace(req.FilePath)
	if filePath == "" {
		return nil, ErrFilePathRequired
	}

	createdByType := strings.ToLower(strings.TrimSpace(req.CreatedByType))
	if createdByType == "" {
		createdByType = systemActorType
	}
	switch createdByType {
	case "human", "agent", "system":
	default:
		return nil, fmt.Errorf("%w: %q", ErrCreatedByTypeInvalid, req.CreatedByType)
	}

	createdByID := req.CreatedByID
	if createdByID == uuid.Nil {
		createdByID = systemActorID
	}

	created, err := s.repo.Create(ctx, repo.Skill{
		OrganizationID: orgID,
		ProjectID:      req.ProjectID,
		Slug:           slug,
		DisplayName:    displayName,
		Description:    strings.TrimSpace(req.Description),
		FilePath:       filePath,
		Version:        1,
		IsActive:       true,
		CreatedByType:  createdByType,
		CreatedByID:    createdByID,
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (s *service) Update(ctx context.Context, skillID uuid.UUID, req UpdateRequest) (*Skill, error) {
	existing, err := s.repo.GetByID(ctx, skillID)
	if err != nil {
		return nil, err
	}

	if req.DisplayName != nil {
		displayName := strings.TrimSpace(*req.DisplayName)
		if displayName == "" {
			return nil, ErrDisplayNameRequired
		}
		if validation.HasHTMLTag(displayName) {
			return nil, ErrDisplayNameInvalid
		}
		existing.DisplayName = displayName
	}
	if req.Description != nil {
		existing.Description = strings.TrimSpace(*req.Description)
	}
	if req.FilePath != nil {
		filePath := strings.TrimSpace(*req.FilePath)
		if filePath == "" {
			return nil, ErrFilePathRequired
		}
		existing.FilePath = filePath
	}

	updated, err := s.repo.Update(ctx, existing)
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *service) Delete(ctx context.Context, skillID uuid.UUID) error {
	_, err := s.repo.SetActive(ctx, skillID, false)
	return err
}

func (s *service) Get(ctx context.Context, skillID uuid.UUID) (*Skill, error) {
	got, err := s.repo.GetByID(ctx, skillID)
	if err != nil {
		return nil, err
	}
	return &got, nil
}

func (s *service) List(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID) ([]*Skill, error) {
	if projectID != nil {
		return nil, ErrProjectScopedUnsupported
	}

	found, err := s.repo.ListByOrg(ctx, orgID, false)
	if err != nil {
		return nil, err
	}

	skills := make([]*Skill, 0, len(found))
	for i := range found {
		skill := found[i]
		skills = append(skills, &skill)
	}
	return skills, nil
}

func (s *service) CheckConsistency(ctx context.Context, orgID uuid.UUID, skillsDir string) (*ConsistencyReport, error) {
	dir := strings.TrimSpace(skillsDir)
	if dir == "" {
		dir = defaultSkillsDir
	}

	skills, err := s.repo.ListByOrg(ctx, orgID, false)
	if err != nil {
		return nil, err
	}

	report := &ConsistencyReport{
		MissingFiles:      []string{},
		UnregisteredFiles: []string{},
		Mismatches:        []string{},
	}

	registered := make(map[string]struct{}, len(skills)*2)
	for _, skill := range skills {
		canonical := normalizePath(skill.FilePath)
		if canonical == "" {
			report.MissingFiles = append(report.MissingFiles, skill.FilePath)
		} else {
			registered[canonical] = struct{}{}
			if strings.HasPrefix(canonical, "skills/") {
				registered[strings.TrimPrefix(canonical, "skills/")] = struct{}{}
			} else {
				registered["skills/"+canonical] = struct{}{}
			}
			if !fileExistsForSkillPath(dir, canonical) {
				report.MissingFiles = append(report.MissingFiles, skill.FilePath)
			}

			base := filepath.Base(canonical)
			filenameSlug := strings.TrimSuffix(base, filepath.Ext(base))
			if filenameSlug != skill.Slug {
				report.Mismatches = append(report.Mismatches, fmt.Sprintf("%s (slug=%s)", skill.FilePath, skill.Slug))
			}
		}
	}

	filesOnDisk, err := markdownFiles(dir)
	if err != nil {
		return nil, err
	}

	for _, file := range filesOnDisk {
		if _, ok := registered[file]; !ok {
			report.UnregisteredFiles = append(report.UnregisteredFiles, file)
		}
	}

	sort.Strings(report.MissingFiles)
	sort.Strings(report.UnregisteredFiles)
	sort.Strings(report.Mismatches)

	return report, nil
}

func normalizePath(path string) string {
	cleaned := strings.TrimSpace(path)
	if cleaned == "" {
		return ""
	}
	cleaned = filepath.ToSlash(filepath.Clean(cleaned))
	cleaned = strings.TrimPrefix(cleaned, "./")
	cleaned = strings.TrimPrefix(cleaned, "/")
	return cleaned
}

func fileExistsForSkillPath(skillsDir, skillPath string) bool {
	if skillPath == "" {
		return false
	}

	trimmed := strings.TrimPrefix(skillPath, "skills/")
	candidates := []string{
		filepath.Join(skillsDir, skillPath),
		filepath.Join(skillsDir, trimmed),
	}

	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func markdownFiles(skillsDir string) ([]string, error) {
	if _, err := os.Stat(skillsDir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	files := make([]string, 0)
	err := filepath.WalkDir(skillsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.ToLower(filepath.Ext(d.Name())) != ".md" {
			return nil
		}

		rel, err := filepath.Rel(skillsDir, path)
		if err != nil {
			return err
		}
		normalized := normalizePath(rel)
		files = append(files, normalized)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(files)
	return files, nil
}
