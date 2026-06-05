package main

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
	"github.com/BloomingProsperity/HUAKAI/internal/paymenthttp"
)

func buildPaymentRefundRequestRecorder(pool *pgxpool.Pool, svc *payment.Service) paymenthttp.RefundRequestRecorder {
	return paymenthttp.NewPostgresRefundRequestRecorder(pool, svc)
}
