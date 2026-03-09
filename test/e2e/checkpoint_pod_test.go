/*
Copyright 2026 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gexec"
)

// hostNetworkSandboxConfig returns a JSON sandbox config that uses host
// networking to avoid CNI plugin dependencies in test environments.
func hostNetworkSandboxConfig(name, uid, namespace string) string {
	return fmt.Sprintf(`{
		"metadata": { "name": %q, "uid": %q, "namespace": %q },
		"linux": { "security_context": { "namespace_options": { "network": 2 } } }
	}`, name, uid, namespace)
}

// createPodSandbox creates a pod sandbox from the given JSON config string
// and returns the pod ID. The caller is responsible for cleanup.
func createPodSandbox(configJSON string) string {
	sb, err := os.CreateTemp("", "sandbox-")
	Expect(err).NotTo(HaveOccurred())
	_, err = fmt.Fprint(sb, configJSON)
	Expect(err).NotTo(HaveOccurred())
	Expect(sb.Close()).NotTo(HaveOccurred())

	res := t.Crictl("runp " + sb.Name())
	Expect(res).To(Exit(0))
	Expect(os.RemoveAll(sb.Name())).NotTo(HaveOccurred())

	return string(bytes.TrimSpace(res.Out.Contents()))
}

// cleanupPod force-stops and removes a pod sandbox, then cleans up pause images.
func cleanupPod(podID string) {
	Expect(t.Crictl("rmp -f " + podID)).To(Exit(0))
	t.CrictlRemovePauseImages()
}

var _ = t.Describe("checkpointpod", func() {
	It("should fail without pod ID", func() {
		t.CrictlExpectFailure("checkpointpod", "", "POD-ID cannot be empty")
	})

	It("should fail without --path flag", func() {
		t.CrictlExpectFailure("checkpointpod some-pod-id", "",
			"cannot checkpoint a pod without specifying the checkpoint path")
	})

	It("should fail with nonexistent pod ID", func() {
		checkpointDir, err := os.MkdirTemp("", "checkpoint-")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(checkpointDir)

		t.CrictlExpectFailure(
			fmt.Sprintf("checkpointpod --path %s nonexistent-pod-id", checkpointDir),
			"", "checkpointing pod")
	})

	It("should fail with multiple pod IDs", func() {
		t.CrictlExpectFailure("checkpointpod --path /tmp/ckpt pod1 pod2", "",
			"only one pod sandbox can be checkpointed at a time")
	})

	It("should checkpoint a pod sandbox with no running containers", func() {
		podID := createPodSandbox(hostNetworkSandboxConfig(
			"ckpt-empty", "uid-ckpt-empty", "default"))
		defer cleanupPod(podID)

		checkpointDir, err := os.MkdirTemp("", "checkpoint-")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(checkpointDir)

		res := t.Crictl(fmt.Sprintf("checkpointpod --path %s %s", checkpointDir, podID))
		Expect(res).To(Exit(0))
		Expect(string(res.Out.Contents())).To(ContainSubstring("checkpointed"))

		// Verify checkpoint files exist.
		_, err = os.Stat(filepath.Join(checkpointDir, "pod-config.json"))
		Expect(err).NotTo(HaveOccurred())
		_, err = os.Stat(filepath.Join(checkpointDir, "checkpoint-manifest.json"))
		Expect(err).NotTo(HaveOccurred())
	})

	It("should write correct pod-config.json content", func() {
		podID := createPodSandbox(hostNetworkSandboxConfig(
			"ckpt-verify-config", "uid-verify-config", "test-ns"))
		defer cleanupPod(podID)

		checkpointDir, err := os.MkdirTemp("", "checkpoint-")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(checkpointDir)

		res := t.Crictl(fmt.Sprintf("checkpointpod --path %s %s", checkpointDir, podID))
		Expect(res).To(Exit(0))

		// Parse and validate pod-config.json.
		configData, err := os.ReadFile(filepath.Join(checkpointDir, "pod-config.json"))
		Expect(err).NotTo(HaveOccurred())

		var config map[string]interface{}
		Expect(json.Unmarshal(configData, &config)).NotTo(HaveOccurred())

		// Verify metadata fields are preserved.
		metadata, ok := config["metadata"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "pod-config.json should have metadata")
		Expect(metadata["name"]).To(Equal("ckpt-verify-config"))
		Expect(metadata["namespace"]).To(Equal("test-ns"))
		Expect(metadata["uid"]).To(Equal("uid-verify-config"))
	})

	It("should write correct checkpoint-manifest.json content", func() {
		podID := createPodSandbox(hostNetworkSandboxConfig(
			"ckpt-verify-manifest", "uid-verify-manifest", "default"))
		defer cleanupPod(podID)

		checkpointDir, err := os.MkdirTemp("", "checkpoint-")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(checkpointDir)

		res := t.Crictl(fmt.Sprintf("checkpointpod --path %s %s", checkpointDir, podID))
		Expect(res).To(Exit(0))

		// Parse and validate checkpoint-manifest.json.
		manifestData, err := os.ReadFile(filepath.Join(checkpointDir, "checkpoint-manifest.json"))
		Expect(err).NotTo(HaveOccurred())

		var manifest struct {
			SandboxID  string   `json:"sandboxId"`
			Containers []string `json:"containers"`
		}
		Expect(json.Unmarshal(manifestData, &manifest)).NotTo(HaveOccurred())
		Expect(manifest.SandboxID).To(Equal(podID))
		// No running containers, so the list should be empty or null.
		Expect(manifest.Containers).To(BeEmpty())
	})

	It("should accept --timeout flag", func() {
		podID := createPodSandbox(hostNetworkSandboxConfig(
			"ckpt-timeout", "uid-ckpt-timeout", "default"))
		defer cleanupPod(podID)

		checkpointDir, err := os.MkdirTemp("", "checkpoint-")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(checkpointDir)

		res := t.Crictl(fmt.Sprintf("checkpointpod --timeout 30 --path %s %s", checkpointDir, podID))
		Expect(res).To(Exit(0))
		Expect(string(res.Out.Contents())).To(ContainSubstring("checkpointed"))
	})

	It("should checkpoint different pods independently", func() {
		podID1 := createPodSandbox(hostNetworkSandboxConfig(
			"ckpt-indep-1", "uid-indep-1", "default"))
		defer cleanupPod(podID1)

		podID2 := createPodSandbox(hostNetworkSandboxConfig(
			"ckpt-indep-2", "uid-indep-2", "default"))
		defer cleanupPod(podID2)

		checkpointDir1, err := os.MkdirTemp("", "checkpoint1-")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(checkpointDir1)

		checkpointDir2, err := os.MkdirTemp("", "checkpoint2-")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(checkpointDir2)

		// Checkpoint both pods.
		res := t.Crictl(fmt.Sprintf("checkpointpod --path %s %s", checkpointDir1, podID1))
		Expect(res).To(Exit(0))
		res = t.Crictl(fmt.Sprintf("checkpointpod --path %s %s", checkpointDir2, podID2))
		Expect(res).To(Exit(0))

		// Verify each checkpoint has the correct sandbox ID.
		manifestData1, err := os.ReadFile(filepath.Join(checkpointDir1, "checkpoint-manifest.json"))
		Expect(err).NotTo(HaveOccurred())
		var manifest1 struct {
			SandboxID string `json:"sandboxId"`
		}
		Expect(json.Unmarshal(manifestData1, &manifest1)).NotTo(HaveOccurred())
		Expect(manifest1.SandboxID).To(Equal(podID1))

		manifestData2, err := os.ReadFile(filepath.Join(checkpointDir2, "checkpoint-manifest.json"))
		Expect(err).NotTo(HaveOccurred())
		var manifest2 struct {
			SandboxID string `json:"sandboxId"`
		}
		Expect(json.Unmarshal(manifestData2, &manifest2)).NotTo(HaveOccurred())
		Expect(manifest2.SandboxID).To(Equal(podID2))
	})

	It("should checkpoint a stopped pod sandbox", func() {
		podID := createPodSandbox(hostNetworkSandboxConfig(
			"ckpt-stopped", "uid-ckpt-stopped", "default"))
		defer cleanupPod(podID)

		// Stop the sandbox first.
		res := t.Crictl("stopp " + podID)
		Expect(res).To(Exit(0))

		checkpointDir, err := os.MkdirTemp("", "checkpoint-")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(checkpointDir)

		// Checkpoint should still succeed (metadata is still available).
		res = t.Crictl(fmt.Sprintf("checkpointpod --path %s %s", checkpointDir, podID))
		Expect(res).To(Exit(0))

		// Verify files exist.
		_, err = os.Stat(filepath.Join(checkpointDir, "pod-config.json"))
		Expect(err).NotTo(HaveOccurred())
		_, err = os.Stat(filepath.Join(checkpointDir, "checkpoint-manifest.json"))
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = t.Describe("restorepod", func() {
	It("should fail without checkpoint path", func() {
		t.CrictlExpectFailure("restorepod", "", "CHECKPOINT-PATH cannot be empty")
	})

	It("should fail with nonexistent checkpoint path", func() {
		t.CrictlExpectFailure("restorepod /nonexistent/path", "",
			"restoring pod")
	})

	It("should fail with multiple checkpoint paths", func() {
		t.CrictlExpectFailure("restorepod /path1 /path2", "",
			"only one checkpoint path can be restored at a time")
	})

	It("should fail with invalid pod-config.json", func() {
		checkpointDir, err := os.MkdirTemp("", "restore-invalid-")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(checkpointDir)

		// Write an invalid pod-config.json.
		Expect(os.WriteFile(
			filepath.Join(checkpointDir, "pod-config.json"),
			[]byte("not valid json"),
			0o600,
		)).NotTo(HaveOccurred())

		t.CrictlExpectFailure(
			"restorepod "+checkpointDir,
			"", "restoring pod")
	})

	It("should restore a pod from a checkpoint", func() {
		// Create and checkpoint a pod.
		podID := createPodSandbox(hostNetworkSandboxConfig(
			"restore-roundtrip", "uid-restore-roundtrip", "default"))

		checkpointDir, err := os.MkdirTemp("", "checkpoint-")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(checkpointDir)

		res := t.Crictl(fmt.Sprintf("checkpointpod --path %s %s", checkpointDir, podID))
		Expect(res).To(Exit(0))

		// Remove the original pod.
		Expect(t.Crictl("rmp -f " + podID)).To(Exit(0))

		// Restore from checkpoint.
		res = t.Crictl("restorepod " + checkpointDir)
		Expect(res).To(Exit(0))
		output := string(bytes.TrimSpace(res.Out.Contents()))
		Expect(output).To(ContainSubstring("Pod restored:"))

		// Extract the new pod ID from output ("Pod restored: <id>").
		parts := bytes.Fields(res.Out.Contents())
		Expect(len(parts)).To(BeNumerically(">=", 3))
		restoredPodID := string(bytes.TrimSpace(parts[len(parts)-1]))
		Expect(restoredPodID).NotTo(BeEmpty())
		Expect(restoredPodID).NotTo(Equal(podID), "restored pod should have a new ID")

		// Verify the restored pod exists and is in Ready state.
		res = t.Crictl("inspectp " + restoredPodID)
		Expect(res).To(Exit(0))

		var podStatus map[string]interface{}
		Expect(json.Unmarshal(res.Out.Contents(), &podStatus)).NotTo(HaveOccurred())
		status, ok := podStatus["status"].(map[string]interface{})
		Expect(ok).To(BeTrue())
		Expect(status["state"]).To(Equal("SANDBOX_READY"))

		// Cleanup restored pod.
		cleanupPod(restoredPodID)
	})

	It("should restore a pod preserving metadata from checkpoint", func() {
		podID := createPodSandbox(hostNetworkSandboxConfig(
			"restore-meta", "uid-restore-meta", "my-namespace"))

		checkpointDir, err := os.MkdirTemp("", "checkpoint-")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(checkpointDir)

		res := t.Crictl(fmt.Sprintf("checkpointpod --path %s %s", checkpointDir, podID))
		Expect(res).To(Exit(0))

		// Remove the original pod.
		Expect(t.Crictl("rmp -f " + podID)).To(Exit(0))

		// Restore from checkpoint.
		res = t.Crictl("restorepod " + checkpointDir)
		Expect(res).To(Exit(0))

		parts := bytes.Fields(res.Out.Contents())
		restoredPodID := string(bytes.TrimSpace(parts[len(parts)-1]))

		// Verify the restored pod has the same metadata.
		res = t.Crictl("inspectp " + restoredPodID)
		Expect(res).To(Exit(0))

		var podInfo map[string]interface{}
		Expect(json.Unmarshal(res.Out.Contents(), &podInfo)).NotTo(HaveOccurred())
		status := podInfo["status"].(map[string]interface{})
		metadata := status["metadata"].(map[string]interface{})
		Expect(metadata["name"]).To(Equal("restore-meta"))
		Expect(metadata["namespace"]).To(Equal("my-namespace"))

		cleanupPod(restoredPodID)
	})
})
