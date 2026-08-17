package schema

import (
	"testing"

	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"github.com/stretchr/testify/require"
)

func TestImageJobParentEdges(t *testing.T) {
	tests := []struct {
		name       string
		field      string
		targetType string
	}{
		{name: "user", field: "user_id", targetType: "User"},
		{name: "api_key", field: "api_key_id", targetType: "APIKey"},
		{name: "group", field: "group_id", targetType: "Group"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			descriptor := requireImageJobEdge(t, tt.name)
			require.Equal(t, tt.field, descriptor.Field)
			require.Equal(t, tt.targetType, descriptor.Type)
			require.True(t, descriptor.Unique)
			require.True(t, descriptor.Required)
			require.False(t, descriptor.Inverse)

			var restrict bool
			for _, annotation := range descriptor.Annotations {
				if sqlAnnotation, ok := annotation.(*entsql.Annotation); ok && sqlAnnotation.OnDelete == entsql.Restrict {
					restrict = true
					break
				}
			}
			require.True(t, restrict, "edge %s should use ON DELETE RESTRICT", tt.name)
		})
	}
}

func requireImageJobEdge(t *testing.T, name string) *edge.Descriptor {
	t.Helper()

	for _, schemaEdge := range (ImageJob{}).Edges() {
		descriptor := schemaEdge.Descriptor()
		if descriptor.Name == name {
			return descriptor
		}
	}

	require.Failf(t, "missing image job edge", "ImageJob should include edge %s", name)
	return nil
}
