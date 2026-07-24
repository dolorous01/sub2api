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

// ImageJobInput holds object-storage metadata for an image job input.
type ImageJobInput struct {
	ent.Schema
}

func (ImageJobInput) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "image_job_inputs"},
	}
}

func (ImageJobInput) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("job_id"),
		field.Int("index"),
		field.String("kind").MaxLen(32),
		field.String("object_key").SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.String("mime_type").MaxLen(128),
		field.Int64("byte_size"),
		field.String("sha256").MaxLen(64),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (ImageJobInput) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("job", ImageJob.Type).
			Ref("inputs").
			Field("job_id").
			Unique().
			Required(),
	}
}

func (ImageJobInput) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("job_id", "kind", "index").
			Unique().
			StorageKey("idx_image_job_inputs_job_kind_index"),
	}
}
