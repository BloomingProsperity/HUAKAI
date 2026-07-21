package balanceledger

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

func TestClassifyAdminBalanceAdjustmentEnforcesThreeIdentityBoundary(t *testing.T) {
	const platformTenantID int64 = 1
	tests := []struct {
		name       string
		input      AdminBalanceAdjustmentInput
		operation  string
		targetKind string
		wantErr    error
	}{
		{
			name:      "部署者给下级租户下发额度",
			input:     AdminBalanceAdjustmentInput{TenantID: 7, Amount: decimal.NewFromInt(10), ActorRole: BalanceActorPlatformAdmin},
			operation: balanceOperationPlatformTenantCredit, targetKind: BalanceTargetTenant,
		},
		{
			name:      "部署者收回下级租户额度",
			input:     AdminBalanceAdjustmentInput{TenantID: 7, Amount: decimal.NewFromInt(-10), ActorRole: BalanceActorPlatformAdmin},
			operation: balanceOperationPlatformTenantDebit, targetKind: BalanceTargetTenant,
		},
		{
			name:      "部署者给直属用户下发额度",
			input:     AdminBalanceAdjustmentInput{TenantID: 1, UserID: 8, Amount: decimal.NewFromInt(10), ActorRole: BalanceActorPlatformAdmin},
			operation: balanceOperationPlatformUserCredit, targetKind: BalanceTargetUser,
		},
		{
			name:    "部署者不得越级动下级租户用户",
			input:   AdminBalanceAdjustmentInput{TenantID: 7, UserID: 8, Amount: decimal.NewFromInt(10), ActorRole: BalanceActorPlatformAdmin},
			wantErr: ErrBalanceAdjustmentForbidden,
		},
		{
			name:      "租户管理员给本租户用户分发",
			input:     AdminBalanceAdjustmentInput{TenantID: 7, UserID: 8, Amount: decimal.NewFromInt(10), ActorRole: BalanceActorTenantOperator, ActorScopeTenantID: 7},
			operation: balanceOperationTenantUserCredit, targetKind: BalanceTargetUser,
		},
		{
			name:      "租户管理员从本租户用户收回",
			input:     AdminBalanceAdjustmentInput{TenantID: 7, UserID: 8, Amount: decimal.NewFromInt(-10), ActorRole: BalanceActorTenantOperator, ActorScopeTenantID: 7},
			operation: balanceOperationTenantUserDebit, targetKind: BalanceTargetUser,
		},
		{
			name:    "租户管理员不得跨租户",
			input:   AdminBalanceAdjustmentInput{TenantID: 8, UserID: 9, Amount: decimal.NewFromInt(10), ActorRole: BalanceActorTenantOperator, ActorScopeTenantID: 7},
			wantErr: ErrBalanceAdjustmentForbidden,
		},
		{
			name:    "租户管理员不得操作平台租户",
			input:   AdminBalanceAdjustmentInput{TenantID: 1, UserID: 9, Amount: decimal.NewFromInt(10), ActorRole: BalanceActorTenantOperator, ActorScopeTenantID: 1},
			wantErr: ErrBalanceAdjustmentForbidden,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operation, targetKind, err := classifyAdminBalanceAdjustment(test.input, platformTenantID)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("err=%v，want %v", err, test.wantErr)
				}
				return
			}
			if err != nil || operation != test.operation || targetKind != test.targetKind {
				t.Fatalf("operation/target/error=%q/%q/%v，want %q/%q/nil", operation, targetKind, err, test.operation, test.targetKind)
			}
		})
	}
}

func TestAdminBalanceAdjustmentFingerprintBindsEconomicMeaning(t *testing.T) {
	base := AdminBalanceAdjustmentInput{
		TenantID: 7, UserID: 8, Amount: decimal.RequireFromString("10.25000000"), CurrencyCode: "USD",
		ActorRole: BalanceActorTenantOperator, ActorScopeTenantID: 7, ActorRef: "admin_user:5", Reason: "分发额度",
	}
	want := adminBalanceAdjustmentFingerprint(base, balanceOperationTenantUserCredit)
	if got := adminBalanceAdjustmentFingerprint(base, balanceOperationTenantUserCredit); got != want {
		t.Fatalf("相同经济事实的指纹不稳定：got=%s want=%s", got, want)
	}
	mutations := []AdminBalanceAdjustmentInput{base, base, base, base}
	mutations[0].UserID++
	mutations[1].Amount = decimal.NewFromInt(11)
	mutations[2].ActorRef = "admin_user:6"
	mutations[3].Reason = "另一原因"
	for index, mutation := range mutations {
		if got := adminBalanceAdjustmentFingerprint(mutation, balanceOperationTenantUserCredit); got == want {
			t.Fatalf("第 %d 个经济事实变更没有改变指纹", index)
		}
	}
}

func TestValidateAdminBalanceAdjustmentAllowsEightDecimalsAndRejectsExcessPrecision(t *testing.T) {
	input := AdminBalanceAdjustmentInput{
		TenantID: 7, UserID: 8, Amount: decimal.RequireFromString("0.12345678"), CurrencyCode: "USD",
		ActorRole: BalanceActorTenantOperator, ActorScopeTenantID: 7, ActorRef: "admin_user:5",
		Reason: "精确分发", IdempotencyKey: "idem-1",
	}
	if err := validateAdminBalanceAdjustmentInput(input); err != nil {
		t.Fatalf("八位小数应允许，got %v", err)
	}
	input.Amount = decimal.RequireFromString("0.123456789")
	if !errors.Is(validateAdminBalanceAdjustmentInput(input), ErrInvalidInput) {
		t.Fatal("超过八位小数必须拒绝")
	}
}
