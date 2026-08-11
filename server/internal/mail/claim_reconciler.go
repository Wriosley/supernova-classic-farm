package mail

import (
	"context"
	"log/slog"
	"time"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
)

const claimReconcileInterval = 5 * time.Second

// ClaimReconciler resumes non-terminal mail claim sagas.
type ClaimReconciler struct {
	orchestrator *ClaimOrchestrator
	now          func() time.Time
	logger       *slog.Logger
}

func NewClaimReconciler(orchestrator *ClaimOrchestrator, now func() time.Time, logger *slog.Logger) *ClaimReconciler {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ClaimReconciler{orchestrator: orchestrator, now: now, logger: logger}
}

func (r *ClaimReconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(claimReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.ReconcileDue(ctx); err != nil {
				r.logger.Error("mail claim reconcile failed", "error", err)
			}
		}
	}
}

func (r *ClaimReconciler) ReconcileDue(ctx context.Context) error {
	if r == nil || r.orchestrator == nil {
		return nil
	}
	rows, err := r.orchestrator.store.ListClaimSagas(ctx)
	if err != nil {
		return err
	}
	nowMS := r.now().UnixMilli()
	var firstErr error
	for _, row := range rows {
		if row == nil {
			continue
		}
		switch row.GetState() {
		case tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_COMPLETED,
			tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_AVAILABLE:
			continue
		}
		if row.GetRetryAtMs() > nowMS {
			continue
		}
		if _, err := r.orchestrator.Advance(ctx, row.GetClaimId()); err != nil {
			r.logger.Warn("mail claim advance failed",
				"mail_id", row.GetMailId(),
				"player_id", row.GetPlayerId(),
				"error", err,
			)
			_ = r.deferRetry(ctx, row.GetClaimId(), err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (r *ClaimReconciler) deferRetry(ctx context.Context, claimID []byte, cause error) error {
	saga, version, err := r.orchestrator.store.GetClaimSaga(ctx, claimID)
	if err != nil {
		return err
	}
	saga.RetryAtMs = r.now().Add(2 * time.Second).UnixMilli()
	saga.LastError = cause.Error()
	saga.UpdatedAtMs = r.now().UnixMilli()
	_, err = r.orchestrator.store.UpdateClaimSaga(ctx, saga, version)
	return err
}
