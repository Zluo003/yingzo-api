package schema

import (
	"fmt"

	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/internal/domain"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// User holds the schema definition for the User entity.
type User struct {
	ent.Schema
}

func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "users"},
	}
}

func (User) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
		mixins.SoftDeleteMixin{},
	}
}

func (User) Fields() []ent.Field {
	return []ent.Field{
		// 唯一约束通过部分索引实现（WHERE deleted_at IS NULL），支持软删除后重用
		// 见迁移文件 016_soft_delete_partial_unique_indexes.sql
		field.String("email").
			MaxLen(255).
			NotEmpty(),
		field.String("password_hash").
			MaxLen(255).
			NotEmpty(),
		field.String("role").
			MaxLen(20).
			Default(domain.RoleUser),
		field.Float("balance").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0),
		field.Float("frozen_balance").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0),
		field.Int("concurrency").
			Default(5),
		field.String("status").
			MaxLen(20).
			Default(domain.StatusActive),

		// Optional profile fields (added later; default '' in DB migration)
		field.String("username").
			MaxLen(100).
			Default(""),
		// wechat field migrated to user_attribute_values (see migration 019)
		field.String("notes").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Default(""),

		// TOTP 双因素认证字段
		field.String("totp_secret_encrypted").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Optional().
			Nillable(),
		field.Bool("totp_enabled").
			Default(false),
		field.Time("totp_enabled_at").
			Optional().
			Nillable(),
		field.String("signup_source").
			Validate(func(value string) error {
				switch value {
				case "email", "linuxdo", "wechat", "oidc", "github", "google", "dingtalk":
					return nil
				default:
					return fmt.Errorf("must be one of email, linuxdo, wechat, oidc, github, google, dingtalk")
				}
			}).
			Default("email"),
		field.Time("last_login_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("last_active_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Bool("restrict_public_groups").Default(false),

		// 余额不足通知
		field.Bool("balance_notify_enabled").
			Default(true),
		field.String("balance_notify_threshold_type").
			Default("fixed"), // "fixed" | "percentage"
		field.Float("balance_notify_threshold").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Optional().
			Nillable(),
		field.String("balance_notify_extra_emails").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Default("[]"),
		field.Float("total_recharged").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0),

		// 用户级每分钟请求数上限（0 = 不限制）。仅当所在分组未设置 rpm_limit 时作为兜底生效。
		field.Int("rpm_limit").
			Default(0),

		// 自助注册时的客户端 IP（与审计日志同源的安全客户端 IP）；管理员后台创建的用户为空。
		field.String("registration_ip").
			MaxLen(64).
			Default(""),
		// 按 IP 注册额度的计数键：IPv4 为地址本身，IPv6 为所在 /64 网段。
		field.String("registration_ip_key").
			MaxLen(64).
			Default(""),
	}
}

func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("api_keys", APIKey.Type),
		edge.To("redeem_codes", RedeemCode.Type),
		edge.To("subscriptions", UserSubscription.Type),
		edge.To("assigned_subscriptions", UserSubscription.Type),
		edge.To("announcement_reads", AnnouncementRead.Type),
		edge.To("allowed_groups", Group.Type).
			Through("user_allowed_groups", UserAllowedGroup.Type),
		edge.To("usage_logs", UsageLog.Type),
		edge.To("video_tasks", VideoTask.Type),
		edge.To("attribute_values", UserAttributeValue.Type),
		edge.To("promo_code_usages", PromoCodeUsage.Type),
		edge.To("payment_orders", PaymentOrder.Type),
		edge.To("auth_identities", AuthIdentity.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("pending_auth_sessions", PendingAuthSession.Type),
		edge.To("platform_quotas", UserPlatformQuota.Type),
	}
}

func (User) Indexes() []ent.Index {
	return []ent.Index{
		// email 字段已在 Fields() 中声明 Unique()，无需重复索引
		index.Fields("status"),
		index.Fields("deleted_at"),
		// 见迁移 270：部分索引（WHERE registration_ip_key 非空），按注册网络统计账号数。
		index.Fields("registration_ip_key", "created_at"),
	}
}
