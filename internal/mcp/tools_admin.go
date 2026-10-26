package mcp

import (
	"context"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"skarb/internal/domain"
	"skarb/internal/money"
	"skarb/internal/service"
	"skarb/internal/store"
)

func (s *Server) addAdminTools(srv *mcpsdk.Server, actor domain.Actor) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "list_categories",
		Description: "List categories with aliases. Archived categories are available to admins only.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listCategoriesIn) (*mcpsdk.CallToolResult, listCategoriesOut, error) {
		cats, err := s.svc.ListCategories(ctx, actor, in.IncludeArchived)
		if err != nil {
			return nil, listCategoriesOut{}, toolError(err)
		}
		out := listCategoriesOut{Categories: make([]categoryOut, 0, len(cats))}
		for _, c := range cats {
			out.Categories = append(out.Categories, categoryToOut(c))
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "create_category",
		Description: "Admin only. Create a category with a kind (income or expense), color, emoji, aliases, and sort order.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in createCategoryIn) (*mcpsdk.CallToolResult, categoryOut, error) {
		kind, err := kindFromString(in.Kind)
		if err != nil {
			return nil, categoryOut{}, err
		}
		out, err := idempotentOut(ctx, s.svc, actor, "mcp.create_category", in.IdempotencyKey, in, func() (categoryOut, error) {
			c, err := s.svc.CreateCategory(ctx, actor, store.CreateCategoryInput{
				Name: in.Name, Kind: kind, Color: in.Color, Emoji: in.Emoji, Aliases: in.Aliases, SortOrder: in.SortOrder,
			})
			if err != nil {
				return categoryOut{}, err
			}
			return categoryToOut(*c), nil
		})
		if err != nil {
			return nil, categoryOut{}, toolError(err)
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "update_category",
		Description: "Admin only. Update a category by UID; kind cannot be changed.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in updateCategoryIn) (*mcpsdk.CallToolResult, categoryOut, error) {
		out, err := idempotentOut(ctx, s.svc, actor, "mcp.update_category", in.IdempotencyKey, in, func() (categoryOut, error) {
			c, err := s.svc.UpdateCategory(ctx, actor, in.UID, store.UpdateCategoryInput{
				Name: in.Name, Color: in.Color, Emoji: in.Emoji, Aliases: in.Aliases, SortOrder: in.SortOrder,
			})
			if err != nil {
				return categoryOut{}, err
			}
			return categoryToOut(*c), nil
		})
		if err != nil {
			return nil, categoryOut{}, toolError(err)
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "delete_category",
		Description: "Admin only. Delete a category; if it is used by transactions, provide reassign_to with another same-kind category UID.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in deleteCategoryIn) (*mcpsdk.CallToolResult, deleteOut, error) {
		out, err := idempotentOut(ctx, s.svc, actor, "mcp.delete_category", in.IdempotencyKey, in, func() (deleteOut, error) {
			if err := s.svc.DeleteCategory(ctx, actor, in.UID, in.ReassignTo); err != nil {
				return deleteOut{}, err
			}
			return deleteOut{Deleted: true, UID: in.UID}, nil
		})
		if err != nil {
			return nil, deleteOut{}, toolError(err)
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "list_accounts",
		Description: "List savings accounts. Archived accounts are available to admins only.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listAccountsIn) (*mcpsdk.CallToolResult, listAccountsOut, error) {
		accounts, err := s.svc.ListAccounts(ctx, actor, in.IncludeArchived)
		if err != nil {
			return nil, listAccountsOut{}, toolError(err)
		}
		out := listAccountsOut{Accounts: make([]accountOut, 0, len(accounts))}
		for _, a := range accounts {
			out.Accounts = append(out.Accounts, accountToOut(a))
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "create_account",
		Description: "Admin only. Create a savings account for an owner. Currency is ISO 4217; kind is bank, cash, investment, or other.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in createAccountIn) (*mcpsdk.CallToolResult, accountOut, error) {
		owner, err := s.resolveUserRef(ctx, in.Owner)
		if err != nil || owner == nil {
			return nil, accountOut{}, validationError("owner is required")
		}
		include := true
		if in.IncludeInNetWorth != nil {
			include = *in.IncludeInNetWorth
		}
		kind, err := parseAccountKind(in.Kind)
		if err != nil {
			return nil, accountOut{}, err
		}
		out, err := idempotentOut(ctx, s.svc, actor, "mcp.create_account", in.IdempotencyKey, in, func() (accountOut, error) {
			a, err := s.svc.CreateAccount(ctx, actor, service.CreateAccountInput{
				OwnerUserID: owner.ID, Name: in.Name, Currency: in.Currency,
				Kind: kind, IncludeInNetWorth: include, SortOrder: in.SortOrder,
			})
			if err != nil {
				return accountOut{}, err
			}
			return accountToOut(*a), nil
		})
		if err != nil {
			return nil, accountOut{}, toolError(err)
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "update_account",
		Description: "Admin only. Update an account by UID. Set archived=true to archive it.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in updateAccountIn) (*mcpsdk.CallToolResult, accountOut, error) {
		upd := service.UpdateAccountInput{
			Name: in.Name, Currency: in.Currency, IncludeInNetWorth: in.IncludeInNetWorth,
			SortOrder: in.SortOrder, Archived: in.Archived,
		}
		if in.Kind != nil {
			k, err := parseAccountKind(*in.Kind)
			if err != nil {
				return nil, accountOut{}, err
			}
			upd.Kind = &k
		}
		out, err := idempotentOut(ctx, s.svc, actor, "mcp.update_account", in.IdempotencyKey, in, func() (accountOut, error) {
			a, err := s.svc.UpdateAccount(ctx, actor, in.UID, upd)
			if err != nil {
				return accountOut{}, err
			}
			return accountToOut(*a), nil
		})
		if err != nil {
			return nil, accountOut{}, toolError(err)
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "delete_account",
		Description: "Admin only. Delete an account with no snapshots. Accounts with history should be archived instead.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in deleteAccountIn) (*mcpsdk.CallToolResult, deleteOut, error) {
		out, err := idempotentOut(ctx, s.svc, actor, "mcp.delete_account", in.IdempotencyKey, in, func() (deleteOut, error) {
			if err := s.svc.DeleteAccount(ctx, actor, in.UID); err != nil {
				return deleteOut{}, err
			}
			return deleteOut{Deleted: true, UID: in.UID}, nil
		})
		if err != nil {
			return nil, deleteOut{}, toolError(err)
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "upsert_snapshot",
		Description: "Admin only. Set a dated balance for an account. Account can be a UID or name; user is an optional owner hint when names are ambiguous.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in upsertSnapshotIn) (*mcpsdk.CallToolResult, snapshotOut, error) {
		ownerHint := in.User
		if u, err := s.resolveUserRef(ctx, in.User); err == nil && u != nil {
			ownerHint = u.DisplayName
		}
		account, err := s.svc.ResolveAccount(ctx, in.Account, ownerHint)
		if err != nil {
			return nil, snapshotOut{}, toolError(err)
		}
		out, err := idempotentOut(ctx, s.svc, actor, "mcp.upsert_snapshot", in.IdempotencyKey, in, func() (snapshotOut, error) {
			snap, err := s.svc.UpsertSnapshot(ctx, actor, service.UpsertSnapshotInput{
				AccountUID: account.UID, Date: in.Date, Amount: in.Amount, Note: in.Note,
			})
			if err != nil {
				return snapshotOut{}, err
			}
			return snapshotToOut(*snap, *account), nil
		})
		if err != nil {
			return nil, snapshotOut{}, toolError(err)
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "delete_snapshot",
		Description: "Admin only. Soft-delete a snapshot by UID.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in deleteSnapshotIn) (*mcpsdk.CallToolResult, deleteOut, error) {
		out, err := idempotentOut(ctx, s.svc, actor, "mcp.delete_snapshot", in.IdempotencyKey, in, func() (deleteOut, error) {
			if err := s.svc.DeleteSnapshot(ctx, actor, in.UID); err != nil {
				return deleteOut{}, err
			}
			return deleteOut{Deleted: true, UID: in.UID}, nil
		})
		if err != nil {
			return nil, deleteOut{}, toolError(err)
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "list_exchange_rates",
		Description: "List NBU exchange rates (UAH per one unit) for a currency and optional date range.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listExchangeRatesIn) (*mcpsdk.CallToolResult, listExchangeRatesOut, error) {
		var from, to *domain.Date
		if strings.TrimSpace(in.From) != "" {
			d, err := domain.ParseDate(in.From)
			if err != nil {
				return nil, listExchangeRatesOut{}, validationError("invalid from date")
			}
			from = &d
		}
		if strings.TrimSpace(in.To) != "" {
			d, err := domain.ParseDate(in.To)
			if err != nil {
				return nil, listExchangeRatesOut{}, validationError("invalid to date")
			}
			to = &d
		}
		rates, err := s.svc.FX.List(ctx, in.Currency, from, to)
		if err != nil {
			return nil, listExchangeRatesOut{}, toolError(err)
		}
		out := listExchangeRatesOut{Rates: make([]rateOut, 0, len(rates))}
		for _, r := range rates {
			out.Rates = append(out.Rates, rateOut{
				Currency: r.Currency, Date: r.RateDate.String(), Rate: money.PlainAmount(r.RateScaled, money.RateScale), Source: r.Source,
			})
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "list_users",
		Description: "List users. Admin sees active/link status and can include inactive users; members see non-sensitive fields.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listUsersIn) (*mcpsdk.CallToolResult, listUsersOut, error) {
		includeInactive := in.IncludeInactive && actor.Role == domain.RoleAdmin
		users, err := s.svc.ListUsers(ctx, actor, includeInactive)
		if err != nil {
			return nil, listUsersOut{}, toolError(err)
		}
		out := listUsersOut{Users: make([]userOut, 0, len(users))}
		for _, u := range users {
			item := userToOut(u)
			if actor.Role != domain.RoleAdmin {
				item.TelegramID = ""
				item.Linked = false
			}
			out.Users = append(out.Users, item)
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "create_user",
		Description: "Admin only. Create a member account. Password is optional; if omitted, a temporary password is generated and returned once.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in createUserIn) (*mcpsdk.CallToolResult, createUserOut, error) {
		out, err := idempotentOut(ctx, s.svc, actor, "mcp.create_user", in.IdempotencyKey, in, func() (createUserOut, error) {
			u, password, err := s.svc.CreateUser(ctx, actor, service.CreateUserInput{
				Username: in.Username, DisplayName: in.DisplayName, Password: in.Password, TelegramUserID: in.TelegramUserID,
			})
			if err != nil {
				return createUserOut{}, err
			}
			return createUserOut{User: userToOut(*u), TemporaryPassword: password}, nil
		})
		if err != nil {
			return nil, createUserOut{}, toolError(err)
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "update_user",
		Description: "Admin only. Update display name, Telegram link, or active status. No password reset or role change via MCP.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in updateUserIn) (*mcpsdk.CallToolResult, userOut, error) {
		out, err := idempotentOut(ctx, s.svc, actor, "mcp.update_user", in.IdempotencyKey, in, func() (userOut, error) {
			target, err := s.svc.UserByUID(ctx, in.UID)
			if err != nil {
				return userOut{}, err
			}
			display := target.DisplayName
			if in.DisplayName != nil {
				display = *in.DisplayName
			}
			telegram := target.TelegramUserID
			if in.TelegramUserID != nil {
				telegram = in.TelegramUserID
			}
			if err := s.svc.UpdateUser(ctx, actor, in.UID, display, telegram); err != nil {
				return userOut{}, err
			}
			if in.Active != nil {
				if err := s.svc.SetUserActive(ctx, actor, in.UID, *in.Active); err != nil {
					return userOut{}, err
				}
			}
			updated, err := s.svc.UserByUID(ctx, in.UID)
			if err != nil {
				return userOut{}, err
			}
			return userToOut(*updated), nil
		})
		if err != nil {
			return nil, userOut{}, toolError(err)
		}
		return nil, out, nil
	})
}
