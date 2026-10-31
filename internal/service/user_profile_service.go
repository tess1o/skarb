package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"skarb/internal/domain"
)

// A nil name leaves the name unchanged. SetTelegram distinguishes unlinking
// (a nil ID) from leaving the existing binding alone.
type userProfileUpdate struct {
	DisplayName *string
	TelegramID  *int64
	SetTelegram bool
}

func (s *Service) UpdateUser(ctx context.Context, actor domain.Actor, uid, displayName string, telegramID *int64) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	user, err := s.Store.GetUserByUID(ctx, uid)
	if err != nil {
		return err
	}
	return s.updateUserProfile(ctx, actor, user, userProfileUpdate{DisplayName: &displayName, TelegramID: telegramID, SetTelegram: true})
}

// UpdateOwnDisplayName never accepts a target identity from the caller.
func (s *Service) UpdateOwnDisplayName(ctx context.Context, actor domain.Actor, name string) error {
	if err := s.RequireAuth(actor); err != nil {
		return err
	}
	user, err := s.Store.GetUserByID(ctx, actor.UserID)
	if err != nil {
		return err
	}
	return s.updateUserProfile(ctx, actor, user, userProfileUpdate{DisplayName: &name})
}

func (s *Service) SetOwnTelegramID(ctx context.Context, actor domain.Actor, id *int64) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	user, err := s.Store.GetUserByID(ctx, actor.UserID)
	if err != nil {
		return err
	}
	return s.updateUserProfile(ctx, actor, user, userProfileUpdate{TelegramID: id, SetTelegram: true})
}

func (s *Service) updateUserProfile(ctx context.Context, actor domain.Actor, user *domain.User, update userProfileUpdate) error {
	if update.DisplayName != nil {
		name := strings.TrimSpace(*update.DisplayName)
		if name == "" {
			return validation("display_name", "Вкажіть ім’я.")
		}
		update.DisplayName = &name
	}
	if update.SetTelegram {
		if err := s.validateTelegramID(ctx, update.TelegramID, user.ID); err != nil {
			return err
		}
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if update.DisplayName != nil {
			if err := s.Store.SetUserDisplayName(ctx, tx, user.ID, *update.DisplayName); err != nil {
				return err
			}
			user.DisplayName = *update.DisplayName
		}
		if update.SetTelegram {
			if err := s.Store.SetUserTelegramID(ctx, tx, user.ID, update.TelegramID); err != nil {
				if errors.Is(err, domain.ErrConflict) {
					return validation("telegram_id", "Цей Telegram ID уже прив’язаний до іншого користувача.")
				}
				return err
			}
			user.TelegramUserID = update.TelegramID
		}
		return s.audit(ctx, tx, actor, "user.updated", "user", user.UID, nil, safeUser(user))
	})
}

func (s *Service) validateTelegramID(ctx context.Context, id *int64, targetID int64) error {
	if id == nil {
		return nil
	}
	if *id <= 0 {
		return validation("telegram_id", "Telegram ID має бути додатним цілим числом.")
	}
	user, err := s.Store.GetUserByTelegramID(ctx, *id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if user.ID != targetID {
		return validation("telegram_id", "Цей Telegram ID уже прив’язаний до іншого користувача.")
	}
	return nil
}
