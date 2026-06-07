package app

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/openclaw/aicrawl/internal/archive"
	"github.com/openclaw/aicrawl/internal/textnorm"
)

type reconcileReport struct {
	Provider              string   `json:"provider"`
	SourceKind            string   `json:"source_kind"`
	SourceConversations   int      `json:"source_conversations"`
	ArchivedConversations int      `json:"archived_conversations"`
	MissingConversations  int      `json:"missing_conversations"`
	SourceMessages        int      `json:"source_messages"`
	ArchivedMessages      int      `json:"archived_messages"`
	MissingMessages       int      `json:"missing_messages"`
	DivergentMessages     int      `json:"divergent_messages"`
	Warnings              []string `json:"warnings,omitempty"`
	NextStep              string   `json:"next_step,omitempty"`
}

func (a *App) reconcile(ctx context.Context, globals globalOptions, args []string) error {
	parsed, err := parseOptions(args, boolSet("json"), valueSet("provider"))
	if err != nil {
		return withExitCode(2, err)
	}
	if len(parsed.positionals) != 1 {
		return withExitCode(2, fmt.Errorf("reconcile requires exactly one official export ZIP or JSON path"))
	}
	provider, err := importProvider(parsed.values["provider"])
	if err != nil {
		return withExitCode(2, err)
	}
	if provider != "auto" && provider != "claude" && provider != "chatgpt" {
		return withExitCode(2, fmt.Errorf("reconcile --provider must be claude, chatgpt, or auto"))
	}
	ar, err := a.openReadOnlyArchive(ctx, globals)
	if err != nil {
		return err
	}
	defer ar.Close()
	report, err := reconcileSource(ctx, ar.DB(), parsed.positionals[0], provider)
	if err != nil {
		return err
	}
	if globals.format == "json" || parsed.bools["json"] {
		return writeJSON(a.stdout, report)
	}
	if err := writeTextLine(a.stdout, "%s reconciliation: %d/%d conversations archived, %d/%d messages archived, %d divergent messages",
		report.Provider,
		report.ArchivedConversations,
		report.SourceConversations,
		report.ArchivedMessages,
		report.SourceMessages,
		report.DivergentMessages,
	); err != nil {
		return err
	}
	if report.NextStep != "" {
		return writeTextLine(a.stdout, "%s", report.NextStep)
	}
	return nil
}

func reconcileSource(ctx context.Context, db *sql.DB, path, provider string) (reconcileReport, error) {
	report := reconcileReport{}
	header, err := streamImportSource(path, provider, func(conversation archive.Conversation, warnings []string) error {
		report.SourceConversations++
		report.SourceMessages += len(conversation.Messages)
		report.Warnings = append(report.Warnings, warnings...)
		exists, err := rowExists(ctx, db, `select 1 from conversations where id = ? limit 1`, conversation.ID)
		if err != nil {
			return err
		}
		if exists {
			report.ArchivedConversations++
		} else {
			report.MissingConversations++
		}
		for _, message := range conversation.Messages {
			archivedText, exists, err := messageText(ctx, db, message.ID)
			if err != nil {
				return err
			}
			if exists {
				report.ArchivedMessages++
				if archivedText != textnorm.Normalize(message.Text) {
					report.DivergentMessages++
				}
			} else {
				report.MissingMessages++
			}
		}
		return nil
	})
	if err != nil {
		return reconcileReport{}, err
	}
	report.Provider = header.Provider
	report.SourceKind = header.SourceKind
	if report.MissingConversations > 0 || report.MissingMessages > 0 || report.DivergentMessages > 0 {
		report.NextStep = "Run `aicrawl import` with this official export to backfill missing rows and refresh divergent message projections."
	}
	return report, nil
}

func messageText(ctx context.Context, db *sql.DB, id string) (string, bool, error) {
	var text string
	err := db.QueryRowContext(ctx, `select text from messages where id = ? limit 1`, id).Scan(&text)
	if err == nil {
		return text, true, nil
	}
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return "", false, err
}

func rowExists(ctx context.Context, db *sql.DB, query, id string) (bool, error) {
	var one int
	err := db.QueryRowContext(ctx, query, id).Scan(&one)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, err
}
