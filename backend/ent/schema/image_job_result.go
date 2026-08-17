package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ImageJobResult holds one indexed output for an image job.
type ImageJobResult struct {
	ent.Schema
}

func (ImageJobResult) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "image_job_results"},
	}
}

func (ImageJobResult) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (ImageJobResult) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("job_id"),
		field.Int("index"),
		field.String("status").MaxLen(32),
		field.String("object_key").SchemaType(map[string]string{dialect.Postgres: "text"}).Optional().Nillable(),
		field.String("mime_type").MaxLen(128).Optional().Nillable(),
		field.Int64("byte_size").Optional().Nillable(),
		field.Int("width").Optional().Nillable(),
		field.Int("height").Optional().Nillable(),
		field.String("size_tier").MaxLen(16).Optional().Nillable(),
		field.String("revised_prompt").SchemaType(map[string]string{dialect.Postgres: "text"}).Optional().Nillable(),
		field.String("upstream_output_id").MaxLen(128).Optional().Nillable(),
	}
}

func (ImageJobResult) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("job", ImageJob.Type).
			Ref("results").
			Field("job_id").
			Unique().
			Required(),
	}
}

func (ImageJobResult) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("job_id", "index").
			Unique().
			StorageKey("idx_image_job_results_job_index"),
	}
}
