package schema

import (
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ImageJob holds a durable asynchronous image operation.
type ImageJob struct {
	ent.Schema
}

func (ImageJob) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "image_jobs"},
	}
}

func (ImageJob) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (ImageJob) Fields() []ent.Field {
	return []ent.Field{
		field.String("public_id").MaxLen(64).Unique(),
		field.Int64("user_id"),
		field.Int64("api_key_id"),
		field.Int64("group_id"),
		field.String("endpoint").MaxLen(64),
		field.String("operation").MaxLen(32),
		field.String("mode").MaxLen(32),
		field.String("requested_model").MaxLen(128),
		field.String("mapped_model").MaxLen(128).Default(""),
		field.String("status").MaxLen(32),
		field.Int("requested_count"),
		field.Int("completed_count").Default(0),
		field.JSON("request", json.RawMessage{}),
		field.String("request_digest").MaxLen(64),
		field.String("idempotency_key_hash").MaxLen(64).Optional().Nillable(),
		field.Float("reserved_usd").SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).Default(0),
		field.Int("reservation_billing_type").Default(1),
		field.Int64("reservation_subscription_id").Optional().Nillable(),
		field.String("reservation_status").MaxLen(20).Default("held"),
		field.JSON("usage", json.RawMessage{}).Optional(),
		field.String("settlement_status").MaxLen(20).Default("pending"),
		field.String("attempt_id").MaxLen(64).Optional().Nillable(),
		field.String("worker_id").MaxLen(128).Optional().Nillable(),
		field.String("execution_phase").MaxLen(20).Default("preflight"),
		field.Time("heartbeat_at").Optional().Nillable(),
		field.Time("cancel_requested_at").Optional().Nillable(),
		field.Time("canceled_at").Optional().Nillable(),
		field.String("error_type").MaxLen(64).Optional().Nillable(),
		field.String("error_code").MaxLen(64).Optional().Nillable(),
		field.String("error_message").SchemaType(map[string]string{dialect.Postgres: "text"}).Optional().Nillable(),
		field.Bool("error_retryable").Default(false),
		field.Time("started_at").Optional().Nillable(),
		field.Time("finished_at").Optional().Nillable(),
		field.Time("expires_at"),
	}
}

func (ImageJob) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("inputs", ImageJobInput.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("results", ImageJobResult.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (ImageJob) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("api_key_id", "idempotency_key_hash").
			Unique().
			StorageKey("idx_image_jobs_api_key_idempotency").
			Annotations(entsql.IndexWhere("idempotency_key_hash IS NOT NULL")),
		index.Fields("status", "created_at", "id").
			StorageKey("idx_image_jobs_claim").
			Annotations(entsql.IndexWhere("status IN ('queued','running')")),
	}
}
