package shard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func objWithLabels(l map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Labels: l}}
}

func TestPropagate(t *testing.T) {
	tests := map[string]struct {
		root  map[string]string
		child map[string]string
		want  map[string]string
	}{
		"stamps onto an unlabeled child": {
			root:  map[string]string{DefaultKey: "a"},
			child: nil,
			want:  map[string]string{DefaultKey: "a"},
		},
		"preserves the child's other labels": {
			root:  map[string]string{DefaultKey: "a"},
			child: map[string]string{"other": "keep"},
			want:  map[string]string{DefaultKey: "a", "other": "keep"},
		},
		"corrects a stale shard on the child": {
			root:  map[string]string{DefaultKey: "a"},
			child: map[string]string{DefaultKey: "b"},
			want:  map[string]string{DefaultKey: "a"},
		},
		"unsharded root leaves the child alone": {
			root:  map[string]string{"other": "x"},
			child: map[string]string{"other": "keep"},
			want:  map[string]string{"other": "keep"},
		},
		"unlabeled root does not create a label map": {
			root:  nil,
			child: nil,
			want:  nil,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			child := objWithLabels(tc.child)
			Propagate(objWithLabels(tc.root), child)
			assert.Equal(t, tc.want, child.GetLabels())
		})
	}
}
