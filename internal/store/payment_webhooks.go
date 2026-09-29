package store

import (
	"github.com/AppsGanin/rospanel/internal/model"
)

// paymentWebhookBodyMax caps the stored body: a provider callback is a few hundred
// bytes, and a flood of junk posted to the URL must not grow the database by megabytes.
const paymentWebhookBodyMax = 16 << 10

// RecordPaymentWebhook stores one provider callback.
func (s *Store) RecordPaymentWebhook(w model.PaymentWebhook) error {
	if len(w.Body) > paymentWebhookBodyMax {
		w.Body = w.Body[:paymentWebhookBodyMax]
	}
	_, err := s.db.Exec(
		`INSERT INTO payment_webhooks (at, provider, remote_ip, provider_id, order_id, status, outcome, error, headers, body)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.At, w.Provider, w.RemoteIP, w.ProviderID, w.OrderID, w.Status, w.Outcome, w.Error, w.Headers, w.Body)
	return err
}

// PaymentWebhookFilter narrows the journal. Zero values mean "any".
type PaymentWebhookFilter struct {
	Provider string
	OrderID  int64
	Failed   bool // only callbacks that did not end in a normal outcome
	Before   int64
	Limit    int
}

// failedOutcomes are the callbacks worth an operator's look.
const failedOutcomes = `('mismatch','no_order','rejected','error')`

// ListPaymentWebhooks returns callbacks newest first. The body and headers ride
// along: the list is short and the operator opens them from it.
func (s *Store) ListPaymentWebhooks(f PaymentWebhookFilter) ([]model.PaymentWebhook, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	q := `SELECT id, at, provider, remote_ip, provider_id, order_id, status, outcome, error, headers, body
	      FROM payment_webhooks WHERE 1=1`
	var args []any
	if f.Provider != "" {
		q += ` AND provider = ?`
		args = append(args, f.Provider)
	}
	if f.OrderID != 0 {
		q += ` AND order_id = ?`
		args = append(args, f.OrderID)
	}
	if f.Failed {
		q += ` AND outcome IN ` + failedOutcomes
	}
	if f.Before > 0 {
		q += ` AND id < ?`
		args = append(args, f.Before)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.PaymentWebhook{}
	for rows.Next() {
		var w model.PaymentWebhook
		if err := rows.Scan(&w.ID, &w.At, &w.Provider, &w.RemoteIP, &w.ProviderID, &w.OrderID,
			&w.Status, &w.Outcome, &w.Error, &w.Headers, &w.Body); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// PurgePaymentWebhooks drops callbacks older than the cutoff, in batches like every
// other sweep.
func (s *Store) PurgePaymentWebhooks(before int64) (int64, error) {
	var total int64
	for {
		res, err := s.db.Exec(
			`DELETE FROM payment_webhooks WHERE id IN (
				SELECT id FROM payment_webhooks WHERE at < ? LIMIT ?
			)`, before, purgeBatch)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if n < purgeBatch {
			return total, nil
		}
	}
}
