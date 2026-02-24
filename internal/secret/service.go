package secret

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	occrypto "github.com/samhotchkiss/otter-camp/internal/crypto"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const currentKeyVersion = 1

var (
	slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

	ErrInvalidSlug          = errors.New("invalid secret slug")
	ErrDisplayNameRequired  = errors.New("display_name is required")
	ErrSecretValueRequired  = errors.New("secret value is required")
	ErrInvalidPrincipalType = errors.New("principal_type must be human, agent, or system")
	ErrSecretInUse          = errors.New("secret is in use")
	ErrInvalidRef           = errors.New("invalid secret reference")
)

type Principal struct {
	Type string
	ID   uuid.UUID
}

type SecretInfo struct {
	ID          uuid.UUID
	Slug        string
	DisplayName string
	Description string
	KeyVersion  int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Service interface {
	Set(ctx context.Context, orgID uuid.UUID, slug, displayName, description, value string, by Principal) error
	Get(ctx context.Context, orgID uuid.UUID, slug string) (string, error)
	List(ctx context.Context, orgID uuid.UUID) ([]*SecretInfo, error)
	Delete(ctx context.Context, orgID uuid.UUID, slug string, by Principal) error
	ResolveRef(ctx context.Context, orgID uuid.UUID, ref string) (string, error)
	CheckDeleteSafety(ctx context.Context, orgID uuid.UUID, slug string) ([]string, error)
}

type Repository interface {
	Create(ctx context.Context, secret repo.Secret) (repo.Secret, error)
	GetBySlug(ctx context.Context, organizationID uuid.UUID, slug string) (repo.Secret, error)
	List(ctx context.Context, organizationID uuid.UUID) ([]repo.Secret, error)
	Update(ctx context.Context, secret repo.Secret) (repo.Secret, error)
	Delete(ctx context.Context, organizationID uuid.UUID, slug string) error
	FindTransportConfigReferences(ctx context.Context, organizationID uuid.UUID, slug string) ([]string, error)
	FindSecretBindingReferences(ctx context.Context, organizationID uuid.UUID, slug string) ([]string, error)
}

type service struct {
	repo          Repository
	loadMasterKey func(version int) (occrypto.MasterKey, error)
}

func NewService(repository Repository) Service {
	return NewServiceWithKeyLoader(repository, occrypto.LoadMasterKey)
}

func NewServiceWithKeyLoader(repository Repository, loader func(version int) (occrypto.MasterKey, error)) Service {
	if repository == nil {
		panic("secret service requires repository")
	}
	if loader == nil {
		panic("secret service requires key loader")
	}
	return &service{
		repo:          repository,
		loadMasterKey: loader,
	}
}

func (s *service) Set(ctx context.Context, orgID uuid.UUID, slug, displayName, description, value string, by Principal) error {
	normalizedSlug, err := normalizeAndValidateSlug(slug)
	if err != nil {
		return err
	}
	normalizedDisplayName := strings.TrimSpace(displayName)
	if normalizedDisplayName == "" {
		return ErrDisplayNameRequired
	}
	if value == "" {
		return ErrSecretValueRequired
	}
	if err := validatePrincipal(by); err != nil {
		return err
	}

	key, err := s.loadMasterKey(currentKeyVersion)
	if err != nil {
		return err
	}
	ciphertext, nonce, err := occrypto.Encrypt(key, []byte(value))
	if err != nil {
		return fmt.Errorf("encrypt secret value: %w", err)
	}

	found, err := s.repo.GetBySlug(ctx, orgID, normalizedSlug)
	if errors.Is(err, repo.ErrNotFound) {
		_, err = s.repo.Create(ctx, repo.Secret{
			OrganizationID: orgID,
			Slug:           normalizedSlug,
			DisplayName:    normalizedDisplayName,
			Description:    description,
			Ciphertext:     ciphertext,
			Nonce:          nonce,
			KeyVersion:     key.Version,
			CreatedByType:  normalizePrincipalType(by.Type),
			CreatedByID:    by.ID,
		})
		return err
	}
	if err != nil {
		return err
	}

	_, err = s.repo.Update(ctx, repo.Secret{
		ID:             found.ID,
		OrganizationID: orgID,
		DisplayName:    normalizedDisplayName,
		Description:    description,
		Ciphertext:     ciphertext,
		Nonce:          nonce,
		KeyVersion:     key.Version,
	})
	return err
}

func (s *service) Get(ctx context.Context, orgID uuid.UUID, slug string) (string, error) {
	normalizedSlug, err := normalizeAndValidateSlug(slug)
	if err != nil {
		return "", err
	}

	found, err := s.repo.GetBySlug(ctx, orgID, normalizedSlug)
	if err != nil {
		return "", err
	}

	key, err := s.loadMasterKey(found.KeyVersion)
	if err != nil {
		return "", err
	}
	plaintext, err := occrypto.Decrypt(key, found.Ciphertext, found.Nonce)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func (s *service) List(ctx context.Context, orgID uuid.UUID) ([]*SecretInfo, error) {
	found, err := s.repo.List(ctx, orgID)
	if err != nil {
		return nil, err
	}

	infos := make([]*SecretInfo, 0, len(found))
	for _, item := range found {
		info := &SecretInfo{
			ID:          item.ID,
			Slug:        item.Slug,
			DisplayName: item.DisplayName,
			Description: item.Description,
			KeyVersion:  item.KeyVersion,
			CreatedAt:   item.CreatedAt,
			UpdatedAt:   item.UpdatedAt,
		}
		infos = append(infos, info)
	}

	return infos, nil
}

func (s *service) Delete(ctx context.Context, orgID uuid.UUID, slug string, by Principal) error {
	normalizedSlug, err := normalizeAndValidateSlug(slug)
	if err != nil {
		return err
	}
	if err := validatePrincipal(by); err != nil {
		return err
	}

	blockers, err := s.CheckDeleteSafety(ctx, orgID, normalizedSlug)
	if err != nil {
		return err
	}
	if len(blockers) > 0 {
		return fmt.Errorf("%w: %s", ErrSecretInUse, strings.Join(blockers, "; "))
	}

	return s.repo.Delete(ctx, orgID, normalizedSlug)
}

func (s *service) ResolveRef(ctx context.Context, orgID uuid.UUID, ref string) (string, error) {
	if !strings.HasPrefix(ref, "ref:") {
		return ref, nil
	}

	slug := strings.TrimPrefix(ref, "ref:")
	if strings.TrimSpace(slug) == "" {
		return "", ErrInvalidRef
	}

	return s.Get(ctx, orgID, slug)
}

func (s *service) CheckDeleteSafety(ctx context.Context, orgID uuid.UUID, slug string) ([]string, error) {
	normalizedSlug, err := normalizeAndValidateSlug(slug)
	if err != nil {
		return nil, err
	}

	transportReferences, err := s.repo.FindTransportConfigReferences(ctx, orgID, normalizedSlug)
	if err != nil {
		return nil, err
	}
	bindingReferences, err := s.repo.FindSecretBindingReferences(ctx, orgID, normalizedSlug)
	if err != nil {
		return nil, err
	}

	references := append([]string{}, transportReferences...)
	references = append(references, bindingReferences...)
	sort.Strings(references)
	return references, nil
}

func normalizeAndValidateSlug(slug string) (string, error) {
	trimmed := strings.TrimSpace(slug)
	if !slugPattern.MatchString(trimmed) {
		return "", fmt.Errorf("%w: %q", ErrInvalidSlug, slug)
	}
	return trimmed, nil
}

func validatePrincipal(principal Principal) error {
	normalizedType := normalizePrincipalType(principal.Type)
	switch normalizedType {
	case "human", "agent", "system":
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrInvalidPrincipalType, principal.Type)
	}
}

func normalizePrincipalType(principalType string) string {
	return strings.ToLower(strings.TrimSpace(principalType))
}
