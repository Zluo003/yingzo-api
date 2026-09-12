package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// VideoTask holds async video generation task state.
type VideoTask struct {
	ent.Schema
}

func (VideoTask) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "video_tasks"},
	}
}

func (VideoTask) Fields() []ent.Field {
	return []ent.Field{
		field.String("public_id").MaxLen(64).NotEmpty().Unique(),
		field.String("request_id").MaxLen(128).Optional().Nillable(),
		field.Int64("user_id"),
		field.Int64("api_key_id"),
		field.Int64("group_id"),
		field.Int64("account_id"),
		field.String("model").MaxLen(100).NotEmpty(),
		field.String("upstream_model").MaxLen(120).NotEmpty(),
		field.String("resolution").MaxLen(16).NotEmpty(),
		field.Int("duration_seconds").Default(0),
		field.Int("reference_duration_seconds").Default(0),
		field.Int("billable_seconds").Default(0),
		field.Float("cost_per_second").
			Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		field.Float("total_cost").
			Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		field.Float("actual_cost").
			Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		field.String("status").MaxLen(32).NotEmpty().Default("queued"),
		field.String("upstream_task_id").MaxLen(128).Optional().Nillable(),
		field.JSON("request_json", map[string]any{}).
			Optional().
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.JSON("upstream_response_json", map[string]any{}).
			Optional().
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.JSON("error_json", map[string]any{}).
			Optional().
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.String("result_video_url").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Time("created_at").
			Default(time.Now).
			Immutable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("completed_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("billed_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("refunded_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (VideoTask) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("video_tasks").
			Field("user_id").
			Required().
			Unique(),
		edge.From("api_key", APIKey.Type).
			Ref("video_tasks").
			Field("api_key_id").
			Required().
			Unique(),
		edge.From("group", Group.Type).
			Ref("video_tasks").
			Field("group_id").
			Required().
			Unique(),
		edge.From("account", Account.Type).
			Ref("video_tasks").
			Field("account_id").
			Required().
			Unique(),
	}
}

func (VideoTask) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("public_id").Unique(),
		index.Fields("user_id"),
		index.Fields("api_key_id"),
		index.Fields("group_id"),
		index.Fields("account_id"),
		index.Fields("status"),
		index.Fields("upstream_task_id"),
		index.Fields("created_at"),
	}
}
