package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Proxy holds the schema definition for the Proxy entity.
type Proxy struct {
	ent.Schema
}

func (Proxy) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "proxies"},
	}
}

func (Proxy) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
		mixins.SoftDeleteMixin{},
	}
}

func (Proxy) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			MaxLen(100).
			NotEmpty(),
		field.String("protocol").
			MaxLen(20).
			NotEmpty(),
		field.String("host").
			MaxLen(255).
			NotEmpty(),
		field.Int("port"),
		field.String("username").
			MaxLen(100).
			Optional().
			Nillable(),
		field.String("password").
			MaxLen(100).
			Optional().
			Nillable(),
		field.String("status").
			MaxLen(20).
			Default("active"),
		field.Time("expires_at").
			Optional().Nillable().
			Comment("Proxy expiration time (NULL means never expires)."),
		field.String("fallback_mode").
			MaxLen(20).Default("none").
			Comment("Fallback target on expiry: none | proxy | direct."),
		field.Int64("backup_proxy_id").
			Optional().Nillable().
			Comment("Backup proxy id when fallback_mode=proxy (self-reference)."),
		field.Int("expiry_warn_days").
			Default(7).
			Comment("Days before expiry to flag as expiring-soon (per proxy)."),
	}
}

// Edges 定义代理实体的关联关系。
func (Proxy) Edges() []ent.Edge {
	return []ent.Edge{
		// accounts: 使用此代理的账户（反向边）
		edge.From("accounts", Account.Type).
			Ref("proxy"),
		// backup_proxy_id 刻意只作为普通字段（不建 edge）：
		//   * edge.To(...).Unique() 是 O2O 语义，会拒绝第二个主代理引用同一备份
		//     （ent: "one of [...] is already connected to a different backup_proxy_id"）；
		//   * 自引用又没法建成 O2M（ent 要求外键落在"多"的那一侧）。
		// 产品语义是"有向且可共享"的引用：多个主代理指向同一备份、主代理还能串成链，
		// 这与迁移 149 建的非唯一索引一致，也由 repository 的 proxy backup reference
		// 集成测试钉住。仓库只用字段读写（SetBackupProxyID / ClearBackupProxyID）。
	}
}

func (Proxy) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status"),
		index.Fields("deleted_at"),
		index.Fields("expires_at"),
		index.Fields("backup_proxy_id"),
	}
}
