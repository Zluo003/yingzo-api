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

// VideoGroupPricingRule stores per-group video pricing by model and resolution.
type VideoGroupPricingRule struct {
	ent.Schema
}

func (VideoGroupPricingRule) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "video_group_pricing_rules"},
	}
}

func (VideoGroupPricingRule) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("group_id"),
		field.String("model_code").MaxLen(100).NotEmpty(),
		field.String("resolution").MaxLen(16).NotEmpty(),
		field.Float("credits_per_second").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		field.Float("reference_video_multiplier").
			Default(1.0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(10,4)"}),
		field.Bool("enabled").Default(true),
		field.Time("created_at").
			Default(time.Now).
			Immutable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (VideoGroupPricingRule) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("group", Group.Type).
			Ref("video_pricing_rules").
			Field("group_id").
			Required().
			Unique(),
	}
}

func (VideoGroupPricingRule) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("group_id"),
		index.Fields("model_code", "resolution"),
		index.Fields("group_id", "model_code", "resolution").Unique(),
	}
}
