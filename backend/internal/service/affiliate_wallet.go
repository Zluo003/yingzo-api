package service

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrAffiliateMinimum = infraerrors.BadRequest("AFFILIATE_MINIMUM_NOT_MET", "未达到最低金额门槛")
var ErrAffiliateInsufficient = infraerrors.BadRequest("AFFILIATE_INSUFFICIENT_BALANCE", "可用返利余额不足")
var ErrAffiliateWithdrawalProcessed = infraerrors.Conflict("AFFILIATE_WITHDRAWAL_PROCESSED", "申请不存在或已处理，请刷新列表")
var ErrAffiliateRefundPending = infraerrors.Conflict("AFFILIATE_REFUND_PENDING", "关联充值退款正在处理中，返利暂不能提现或转换，请稍后重试")

type AffiliateWalletRecord struct {
	SourceOrderID  *int64     `json:"source_order_id,omitempty"`
	ID             int64      `json:"id"`
	UserID         int64      `json:"user_id"`
	Email          string     `json:"email"`
	Amount         float64    `json:"amount"`
	PayAmount      float64    `json:"pay_amount"`
	RebateAmount   float64    `json:"rebate_amount"`
	Action         string     `json:"action"`
	Status         string     `json:"status"`
	AlipayAccount  string     `json:"alipay_account"`
	AlipayName     string     `json:"alipay_name"`
	AdminNote      string     `json:"admin_note"`
	CreatedAt      time.Time  `json:"created_at"`
	ProcessedAt    *time.Time `json:"processed_at,omitempty"`
	FrozenUntil    *time.Time `json:"frozen_until,omitempty"`
	ReversedAmount float64    `json:"reversed_amount"`
}

type AffiliateRefundAdjustment struct {
	RebateRevoked    float64 `json:"rebate_revoked"`
	FrozenRevoked    float64 `json:"frozen_revoked"`
	AvailableRevoked float64 `json:"available_revoked"`
	DebtIncreased    float64 `json:"debt_increased"`
	BonusRevoked     float64 `json:"bonus_revoked"`
}

type AffiliateRefundRepository interface {
	ReverseOrderRewards(context.Context, int64, float64, float64) (*AffiliateRefundAdjustment, error)
}

func (s *AffiliateService) ReverseOrderRewards(ctx context.Context, orderID int64, refundAmount, orderAmount float64) (*AffiliateRefundAdjustment, error) {
	repo, ok := s.repo.(AffiliateRefundRepository)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "affiliate refund accounting unavailable")
	}
	return repo.ReverseOrderRewards(ctx, orderID, refundAmount, orderAmount)
}

type AffiliateWalletRepository interface {
	TransferQuotaToBalanceMinimum(context.Context, int64, float64) (float64, float64, error)
	CreateAffiliateWithdrawal(context.Context, int64, float64, float64, string, string) (*AffiliateWalletRecord, error)
	ProcessAffiliateWithdrawal(context.Context, int64, int64, string, string) error
	ListAffiliateWalletRecords(context.Context, int64, string, int, int) ([]AffiliateWalletRecord, int64, error)
}

func (s *SettingService) AffiliateAmountSetting(ctx context.Context, key string, fallback float64) float64 {
	if s == nil || s.settingRepo == nil {
		return fallback
	}
	raw, err := s.settingRepo.GetValue(ctx, key)
	if err != nil {
		return fallback
	}
	v, err := parseAffiliateAmount(raw)
	if err != nil || (key == SettingKeyAffiliateFirstRechargeBonusPercent && v > 100) {
		return fallback
	}
	return v
}

func parseAffiliateAmount(raw string) (float64, error) {
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100000000 {
		return 0, ErrAffiliateMinimum
	}
	return v, nil
}

func (s *AffiliateService) RequestWithdrawal(ctx context.Context, userID int64, amount float64, account, name string) (*AffiliateWalletRecord, error) {
	if !s.IsEnabled(ctx) {
		return nil, infraerrors.Forbidden("AFFILIATE_DISABLED", "邀请返利活动已关闭，可将返利转为个人余额")
	}
	account, name = strings.TrimSpace(account), strings.TrimSpace(name)
	if account == "" || name == "" || len([]rune(account)) > 128 || len([]rune(name)) > 64 || amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) || amount > 100000000 || math.Abs(amount-roundTo(amount, 2)) > 1e-8 {
		return nil, infraerrors.BadRequest("INVALID_WITHDRAWAL", "请填写有效的提现金额（精确到分）、支付宝账号和实名姓名")
	}
	repo, ok := s.repo.(AffiliateWalletRepository)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "affiliate wallet unavailable")
	}
	minimum := s.settingService.AffiliateAmountSetting(ctx, SettingKeyAffiliateWithdrawMinimum, 0)
	if amount < minimum {
		return nil, ErrAffiliateMinimum
	}
	return repo.CreateAffiliateWithdrawal(ctx, userID, amount, minimum, account, name)
}

func (s *AffiliateService) ProcessWithdrawal(ctx context.Context, id, adminID int64, status, note string) error {
	if (status != "paid" && status != "rejected") || len([]rune(note)) > 500 {
		return infraerrors.BadRequest("INVALID_STATUS", "仅可标记为已支付或驳回，备注最多 500 字")
	}
	repo, ok := s.repo.(AffiliateWalletRepository)
	if !ok {
		return infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "affiliate wallet unavailable")
	}
	return repo.ProcessAffiliateWithdrawal(ctx, id, adminID, status, strings.TrimSpace(note))
}

func (s *AffiliateService) WalletRecords(ctx context.Context, userID int64, kind string, page, size int, admin bool) ([]AffiliateWalletRecord, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	if kind != "recharges" && kind != "ledger" && kind != "withdrawals" && kind != "invites" {
		return nil, 0, infraerrors.BadRequest("INVALID_RECORD_KIND", "invalid record kind")
	}
	if userID <= 0 && (!admin || kind != "withdrawals") {
		return nil, 0, infraerrors.BadRequest("INVALID_USER", "invalid user")
	}
	repo, ok := s.repo.(AffiliateWalletRepository)
	if !ok {
		return nil, 0, infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "affiliate wallet unavailable")
	}
	items, total, err := repo.ListAffiliateWalletRecords(ctx, userID, kind, page, size)
	if !admin {
		for i := range items {
			items[i].Email = maskEmail(items[i].Email)
		}
	}
	return items, total, err
}
