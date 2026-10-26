package mcp

import (
	"context"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"skarb/internal/domain"
	"skarb/internal/service"
)

func (s *Server) addTransactionTools(srv *mcpsdk.Server, actor domain.Actor) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "list_transactions",
		Description: "List transactions with optional date, user, category, kind, source, and search filters. Amounts are decimal strings and dates are ISO YYYY-MM-DD.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listTransactionsIn) (*mcpsdk.CallToolResult, listTransactionsOut, error) {
		f, offset, err := s.transactionFilter(ctx, in, actor)
		if err != nil {
			return nil, listTransactionsOut{}, toolError(err)
		}
		rows, total, err := s.svc.ListTransactions(ctx, actor, f)
		if err != nil {
			return nil, listTransactionsOut{}, toolError(err)
		}
		out := listTransactionsOut{Transactions: make([]transactionOut, 0, len(rows)), Total: total}
		for _, t := range rows {
			out.Transactions = append(out.Transactions, transactionToOut(t))
		}
		if offset+len(rows) < total {
			out.NextCursor = encodeCursor(offset + len(rows))
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "add_transactions",
		Description: "Add 1-10 transactions. Category accepts an existing category UID, name, or alias. Currency defaults to UAH. Date defaults to today. Admins may set the owner; members add for themselves.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in addTransactionsIn) (*mcpsdk.CallToolResult, addTransactionsOut, error) {
		if len(in.Transactions) == 0 {
			return nil, addTransactionsOut{}, validationError("transactions are required")
		}
		if len(in.Transactions) > 10 {
			return nil, addTransactionsOut{}, validationError("at most 10 transactions")
		}
		ownerID, err := s.resolveOwner(ctx, actor, in.User)
		if err != nil {
			return nil, addTransactionsOut{}, toolError(err)
		}
		items := make([]service.TransactionItemInput, 0, len(in.Transactions))
		for _, item := range in.Transactions {
			items = append(items, service.TransactionItemInput{
				Amount: item.Amount, Currency: item.Currency, Date: item.Date,
				Category: item.Category, Description: item.Description, Note: item.Note,
				OwnerUserID: ownerID,
			})
		}
		out, err := idempotentOut(ctx, s.svc, actor, "mcp.add_transactions", in.IdempotencyKey, in, func() (addTransactionsOut, error) {
			originalText := in.OriginalText
			if runes := []rune(originalText); len(runes) > 2000 {
				originalText = string(runes[:2000])
			}
			created, err := s.svc.CreateTransactions(ctx, actor, service.CreateTransactionsInput{
				Items: items, Source: actor.Kind, OriginalText: originalText,
			})
			if err != nil {
				return addTransactionsOut{}, err
			}
			out := addTransactionsOut{Created: make([]transactionOut, 0, len(created))}
			for _, t := range created {
				out.Created = append(out.Created, transactionToOut(t))
			}
			return out, nil
		})
		if err != nil {
			return nil, addTransactionsOut{}, toolError(err)
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "update_transaction",
		Description: "Update a transaction by UID. Any authenticated user may edit any transaction; only admins may change the owner. Amounts are decimal strings, dates ISO YYYY-MM-DD.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in updateTransactionIn) (*mcpsdk.CallToolResult, transactionOut, error) {
		if strings.TrimSpace(in.UID) == "" {
			return nil, transactionOut{}, validationError("uid is required")
		}
		upd := service.UpdateTransactionInput{
			Amount: in.Amount, Currency: in.Currency, Date: in.Date,
			Category: in.Category, Description: in.Description, Note: in.Note,
		}
		if in.User != nil {
			target, err := s.resolveUserRef(ctx, *in.User)
			if err != nil {
				return nil, transactionOut{}, toolError(err)
			}
			if target == nil {
				return nil, transactionOut{}, validationError("user not found")
			}
			if actor.Role != domain.RoleAdmin && target.ID != actor.UserID {
				return nil, transactionOut{}, toolError(domain.ErrForbidden)
			}
			if target.ID != actor.UserID {
				upd.OwnerUserID = &target.ID
			}
		}
		out, err := idempotentOut(ctx, s.svc, actor, "mcp.update_transaction", in.IdempotencyKey, in, func() (transactionOut, error) {
			t, err := s.svc.UpdateTransaction(ctx, actor, in.UID, upd)
			if err != nil {
				return transactionOut{}, err
			}
			return transactionToOut(*t), nil
		})
		if err != nil {
			return nil, transactionOut{}, toolError(err)
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "delete_transaction",
		Description: "Soft-delete a transaction by UID. Any authenticated user may delete any transaction.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in deleteTransactionIn) (*mcpsdk.CallToolResult, deleteOut, error) {
		out, err := idempotentOut(ctx, s.svc, actor, "mcp.delete_transaction", in.IdempotencyKey, in, func() (deleteOut, error) {
			if err := s.svc.DeleteTransaction(ctx, actor, in.UID, in.Reason); err != nil {
				return deleteOut{}, err
			}
			return deleteOut{Deleted: true, UID: in.UID}, nil
		})
		if err != nil {
			return nil, deleteOut{}, toolError(err)
		}
		return nil, out, nil
	})
}
