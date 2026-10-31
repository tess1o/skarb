package service

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"skarb/internal/auth"
	"skarb/internal/domain"
	"skarb/internal/store"
)

const sessionTTL = 30 * 24 * time.Hour

var usernameRE = regexp.MustCompile(`^[a-zA-Z0-9._-]{3,32}$`)

type CreateUserInput struct {
	Username       string
	DisplayName    string
	Password       string
	TelegramUserID *int64
}

func (s *Service) BootstrapAdmin(ctx context.Context) error {
	if s.Cfg.AdminUsername == "" || s.Cfg.AdminPassword == "" {
		return nil
	}
	n, err := s.Store.UserCount(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if err := auth.ValidatePassword(s.Cfg.AdminPassword); err != nil {
		return fmt.Errorf("SKARB_ADMIN_PASSWORD: %w", err)
	}
	hash, err := auth.HashPassword(s.Cfg.AdminPassword)
	if err != nil {
		return err
	}
	actor := s.SystemActor()
	user, err := s.Store.CreateUser(ctx, nil, newUserInput(s.Cfg.AdminUsername, "Admin", hash, domain.RoleAdmin, nil, false))
	if err != nil {
		return fmt.Errorf("create bootstrap admin: %w", err)
	}
	return s.audit(ctx, nil, actor, "admin.bootstrap", "user", user.UID, nil, safeUser(user))
}

func newUserInput(username, displayName, hash string, role domain.Role, tg *int64, mustChange bool) store.CreateUserInput {
	return store.CreateUserInput{
		Username:           username,
		DisplayName:        displayName,
		PasswordHash:       hash,
		Role:               role,
		TelegramUserID:     tg,
		MustChangePassword: mustChange,
	}
}

func safeUser(u *domain.User) map[string]any {
	if u == nil {
		return nil
	}
	return map[string]any{
		"uid":          u.UID,
		"username":     u.Username,
		"display_name": u.DisplayName,
		"role":         u.Role,
		"is_active":    u.IsActive,
		"telegram_id":  u.TelegramUserID,
	}
}

func (s *Service) Login(ctx context.Context, username, password, ip, ua string) (*domain.Session, string, error) {
	username = strings.TrimSpace(username)
	auditActor := domain.Actor{Kind: domain.SourceWeb, IP: ip, UserAgent: ua}
	user, err := s.Store.GetUserByUsername(ctx, username)
	if err != nil || !user.IsActive {
		_ = s.audit(ctx, nil, auditActor, "login.failed", "user", username, nil, nil)
		return nil, "", domain.ErrUnauthorized
	}
	ok, needsRehash, err := auth.VerifyPassword(password, user.PasswordHash)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		_ = s.audit(ctx, nil, auditActor, "login.failed", "user", username, nil, nil)
		return nil, "", domain.ErrUnauthorized
	}
	if needsRehash {
		if newHash, err := auth.HashPassword(password); err == nil {
			_ = s.Store.SetUserPassword(ctx, nil, user.ID, newHash, user.MustChangePassword)
		}
	}
	token, err := auth.NewSessionToken()
	if err != nil {
		return nil, "", err
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		return nil, "", err
	}
	expires := s.Now().Add(sessionTTL)
	if err := s.Store.CreateSession(ctx, auth.HashToken(token), user.ID, csrf, expires, ip, ua); err != nil {
		return nil, "", err
	}
	actor := s.ActorForUser(user, domain.SourceWeb)
	actor.IP, actor.UserAgent = ip, ua
	_ = s.audit(ctx, nil, actor, "login.success", "user", user.UID, nil, nil)
	sess := &domain.Session{
		TokenHash:  auth.HashToken(token),
		UserID:     user.ID,
		CSRFSecret: csrf,
		CreatedAt:  s.Now(),
		LastSeenAt: s.Now(),
		ExpiresAt:  expires,
		IP:         ip,
		UserAgent:  ua,
		User:       *user,
	}
	return sess, token, nil
}

func (s *Service) AuthenticateSession(ctx context.Context, token string) (*domain.Session, error) {
	if token == "" {
		return nil, domain.ErrUnauthorized
	}
	sess, err := s.Store.GetSession(ctx, auth.HashToken(token))
	if err != nil {
		return nil, domain.ErrUnauthorized
	}
	now := s.Now()
	if sess.RevokedAt != nil || !sess.ExpiresAt.After(now) || !sess.User.IsActive {
		return nil, domain.ErrUnauthorized
	}
	expires := now.Add(sessionTTL)
	if err := s.Store.TouchSession(ctx, sess.TokenHash, expires); err == nil {
		sess.ExpiresAt = expires
		sess.LastSeenAt = now
	}
	return sess, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.Store.RevokeSession(ctx, auth.HashToken(token))
}

func (s *Service) ChangePassword(ctx context.Context, actor domain.Actor, oldPassword, newPassword string) error {
	if err := s.RequireAuth(actor); err != nil {
		return err
	}
	user, err := s.Store.GetUserByID(ctx, actor.UserID)
	if err != nil {
		return err
	}
	ok, _, err := auth.VerifyPassword(oldPassword, user.PasswordHash)
	if err != nil || !ok {
		return domain.ErrUnauthorized
	}
	if err := auth.ValidatePassword(newPassword); err != nil {
		return validation("new_password", err.Error())
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.Store.SetUserPassword(ctx, nil, user.ID, hash, false); err != nil {
		return err
	}
	_ = s.Store.RevokeUserSessions(ctx, user.ID)
	_ = s.Store.RevokeAllUserAPITokens(ctx, user.ID)
	return s.audit(ctx, nil, actor, "user.password_changed", "user", user.UID, nil, nil)
}

func (s *Service) CreateUser(ctx context.Context, actor domain.Actor, in CreateUserInput) (*domain.User, string, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, "", err
	}
	in.Username = strings.TrimSpace(in.Username)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if !usernameRE.MatchString(in.Username) {
		return nil, "", validation("username", "3-32 characters: letters, digits, dot, underscore, hyphen")
	}
	if in.DisplayName == "" {
		return nil, "", validation("display_name", "display name is required")
	}
	if in.Password == "" {
		generated, err := auth.NewToken(12)
		if err != nil {
			return nil, "", err
		}
		in.Password = generated
	}
	if err := auth.ValidatePassword(in.Password); err != nil {
		return nil, "", validation("password", err.Error())
	}
	if err := s.validateTelegramID(ctx, in.TelegramUserID, 0); err != nil {
		return nil, "", err
	}
	count, err := s.Store.CountActiveUsers(ctx, nil, 0)
	if err != nil {
		return nil, "", err
	}
	if count >= s.Cfg.MaxActiveUsers {
		return nil, "", fmt.Errorf("%w: maximum active users reached", domain.ErrConflict)
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, "", err
	}
	var user *domain.User
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		user, err = s.Store.CreateUser(ctx, tx, newUserInput(in.Username, in.DisplayName, hash, domain.RoleMember, in.TelegramUserID, true))
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "user.created", "user", user.UID, nil, safeUser(user))
	})
	if err != nil {
		return nil, "", err
	}
	return user, in.Password, nil
}

func (s *Service) ListUsers(ctx context.Context, actor domain.Actor, includeInactive bool) ([]domain.User, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	if includeInactive && actor.Role != domain.RoleAdmin {
		return nil, domain.ErrForbidden
	}
	return s.Store.ListUsers(ctx, includeInactive)
}

func (s *Service) SetUserActive(ctx context.Context, actor domain.Actor, uid string, active bool) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	target, err := s.Store.GetUserByUID(ctx, uid)
	if err != nil {
		return err
	}
	if target.Role == domain.RoleAdmin && !active {
		return validation("active", "the admin account cannot be deactivated")
	}
	if !active && target.ID == actor.UserID {
		return validation("active", "you cannot deactivate your own account")
	}
	if active {
		count, err := s.Store.CountActiveUsers(ctx, nil, target.ID)
		if err != nil {
			return err
		}
		if count >= s.Cfg.MaxActiveUsers {
			return fmt.Errorf("%w: maximum active users reached", domain.ErrConflict)
		}
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if err := s.Store.SetUserActive(ctx, tx, target.ID, active); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "user.active_changed", "user", target.UID, safeUser(target), map[string]any{"active": active})
	})
	if err != nil {
		return err
	}
	if !active {
		_ = s.Store.RevokeUserSessions(ctx, target.ID)
		_ = s.Store.RevokeAllUserAPITokens(ctx, target.ID)
	}
	return nil
}

func (s *Service) ResetUserPassword(ctx context.Context, actor domain.Actor, uid string) (string, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return "", err
	}
	target, err := s.Store.GetUserByUID(ctx, uid)
	if err != nil {
		return "", err
	}
	password, err := auth.NewToken(12)
	if err != nil {
		return "", err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return "", err
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if err := s.Store.SetUserPassword(ctx, tx, target.ID, hash, true); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "user.password_reset", "user", target.UID, nil, nil)
	})
	if err != nil {
		return "", err
	}
	_ = s.Store.RevokeUserSessions(ctx, target.ID)
	_ = s.Store.RevokeAllUserAPITokens(ctx, target.ID)
	return password, nil
}

type APITokenCreateInput struct {
	TargetUserUID string
	Name          string
	ExpiresAt     *time.Time
}

func (s *Service) CreateAPIToken(ctx context.Context, actor domain.Actor, in APITokenCreateInput) (string, *domain.APIToken, error) {
	if err := s.RequireAuth(actor); err != nil {
		return "", nil, err
	}
	targetID := actor.UserID
	if in.TargetUserUID != "" && in.TargetUserUID != actor.UserUID {
		if actor.Role != domain.RoleAdmin {
			return "", nil, domain.ErrForbidden
		}
		target, err := s.Store.GetUserByUID(ctx, in.TargetUserUID)
		if err != nil {
			return "", nil, err
		}
		targetID = target.ID
	}
	if strings.TrimSpace(in.Name) == "" {
		return "", nil, validation("name", "token name is required")
	}
	tok, err := auth.NewAPIToken()
	if err != nil {
		return "", nil, err
	}
	token, err := s.Store.CreateAPIToken(ctx, store.CreateAPITokenInput{
		TokenHash:   tok.Hash,
		TokenPrefix: tok.Prefix,
		Last4:       tok.Last4,
		UserID:      targetID,
		Name:        strings.TrimSpace(in.Name),
		ExpiresAt:   in.ExpiresAt,
	})
	if err != nil {
		return "", nil, err
	}
	_ = s.audit(ctx, nil, actor, "api_token.created", "api_token", token.UID, nil, map[string]any{"name": token.Name})
	return tok.Plaintext, token, nil
}

func (s *Service) ListAPITokens(ctx context.Context, actor domain.Actor, targetUserUID string) ([]domain.APIToken, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	if targetUserUID == "" || targetUserUID == actor.UserUID {
		id := actor.UserID
		return s.Store.ListAPITokens(ctx, &id)
	}
	if actor.Role != domain.RoleAdmin {
		return nil, domain.ErrForbidden
	}
	target, err := s.Store.GetUserByUID(ctx, targetUserUID)
	if err != nil {
		return nil, err
	}
	return s.Store.ListAPITokens(ctx, &target.ID)
}

func (s *Service) ListAllAPITokens(ctx context.Context, actor domain.Actor) ([]domain.APIToken, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	return s.Store.ListAPITokens(ctx, nil)
}

func (s *Service) RevokeAPIToken(ctx context.Context, actor domain.Actor, tokenUID string) error {
	if err := s.RequireAuth(actor); err != nil {
		return err
	}
	token, err := s.Store.GetAPITokenByUID(ctx, tokenUID)
	if err != nil {
		return err
	}
	if token.UserID != actor.UserID && actor.Role != domain.RoleAdmin {
		return domain.ErrForbidden
	}
	if err := s.Store.RevokeAPIToken(ctx, token.UID, token.UserID); err != nil {
		return err
	}
	return s.audit(ctx, nil, actor, "api_token.revoked", "api_token", token.UID, nil, nil)
}

func (s *Service) AuthenticateAPIToken(ctx context.Context, plaintext string) (*domain.APIToken, error) {
	if plaintext == "" {
		return nil, domain.ErrUnauthorized
	}
	token, err := s.Store.GetAPITokenByHash(ctx, auth.HashToken(plaintext))
	if err != nil {
		return nil, domain.ErrUnauthorized
	}
	now := s.Now()
	if token.RevokedAt != nil || !token.User.IsActive {
		return nil, domain.ErrUnauthorized
	}
	if token.ExpiresAt != nil && !token.ExpiresAt.After(now) {
		return nil, domain.ErrUnauthorized
	}
	_ = s.Store.TouchAPIToken(ctx, token.ID)
	return token, nil
}

func (s *Service) UserByUID(ctx context.Context, uid string) (*domain.User, error) {
	return s.Store.GetUserByUID(ctx, uid)
}

// ResetAdminPassword applies the configured admin password for lockout recovery.
func (s *Service) ResetAdminPassword(ctx context.Context) (string, error) {
	if s.Cfg.AdminUsername == "" || s.Cfg.AdminPassword == "" {
		return "", fmt.Errorf("SKARB_ADMIN_USERNAME and SKARB_ADMIN_PASSWORD are required")
	}
	if err := auth.ValidatePassword(s.Cfg.AdminPassword); err != nil {
		return "", err
	}
	admin, err := s.Store.AdminUser(ctx)
	if err != nil {
		if n, countErr := s.Store.UserCount(ctx); countErr == nil && n == 0 {
			if err := s.BootstrapAdmin(ctx); err != nil {
				return "", err
			}
			return s.Cfg.AdminUsername, nil
		}
		return "", fmt.Errorf("admin account not found: %w", err)
	}
	hash, err := auth.HashPassword(s.Cfg.AdminPassword)
	if err != nil {
		return "", err
	}
	if err := s.Store.SetUserPassword(ctx, nil, admin.ID, hash, false); err != nil {
		return "", err
	}
	_ = s.Store.RevokeUserSessions(ctx, admin.ID)
	_ = s.Store.RevokeAllUserAPITokens(ctx, admin.ID)
	_ = s.audit(ctx, nil, s.SystemActor(), "admin.password_reset", "user", admin.UID, nil, nil)
	return admin.Username, nil
}
