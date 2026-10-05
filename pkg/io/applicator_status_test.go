package io_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk/pkg/io"
)

var _ = DescribeTable("status optimistic locking", func(update bool) {
	applicator := io.NewAPIPatchingApplicator(c)
	opts := []io.ApplyOption{}
	if update {
		opts = append(opts, io.AsUpdate())
	}
	lockedOpts := append(append([]io.ApplyOption{}, opts...), io.WithOptimisticLock())
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "status-lock-", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "test", Image: "test"}}},
	}
	Expect(c.Create(ctx, pod)).To(Succeed())
	Expect(c.Get(ctx, client.ObjectKeyFromObject(pod), pod)).To(Succeed())
	stale := pod.DeepCopy()
	originalVersion := stale.ResourceVersion

	By("accepting the current version and returning the successful write's version")
	pod.Status.Phase = corev1.PodSucceeded
	Expect(applicator.ApplyStatus(ctx, pod, lockedOpts...)).To(Succeed())
	stored := &corev1.Pod{}
	Expect(c.Get(ctx, client.ObjectKeyFromObject(pod), stored)).To(Succeed())
	Expect(stored.Status.Phase).To(Equal(corev1.PodSucceeded))
	terminalStatus := stored.Status.DeepCopy()

	By("rejecting a stale nonterminal decision without changing terminal status")
	stale.Status.Phase = corev1.PodPending
	err := applicator.ApplyStatus(ctx, stale, lockedOpts...)
	Expect(errors.IsConflict(err)).To(BeTrue(), "expected conflict, got %v", err)
	Expect(stale.ResourceVersion).To(Equal(originalVersion))
	Expect(c.Get(ctx, client.ObjectKeyFromObject(pod), stored)).To(Succeed())
	Expect(stored.Status).To(Equal(*terminalStatus))
	Expect(pod.ResourceVersion).To(Equal(stored.ResourceVersion))
	Expect(pod.ResourceVersion).NotTo(Equal(originalVersion))

	By("not advancing a stale version when the status write is a no-op")
	noop := stale.DeepCopy()
	noop.Status = *terminalStatus.DeepCopy()
	Expect(applicator.ApplyStatus(ctx, noop, lockedOpts...)).To(Succeed())
	Expect(noop.ResourceVersion).To(Equal(originalVersion))

	By("requiring a version for an optimistic status write")
	missing := stale.DeepCopy()
	missing.ResourceVersion = ""
	Expect(applicator.ApplyStatus(ctx, missing, lockedOpts...)).To(MatchError(io.ResourceVersionMissing{}))

	By("allowing a missing version when optimistic locking is not requested")
	missing.Status.Phase = corev1.PodRunning
	Expect(applicator.ApplyStatus(ctx, missing, opts...)).To(Succeed())
	Expect(c.Get(ctx, client.ObjectKeyFromObject(pod), stored)).To(Succeed())
	Expect(stored.Status.Phase).To(Equal(corev1.PodRunning))
},
	Entry("patch", false),
	Entry("update", true),
)
