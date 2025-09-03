package controller

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	komposev1alpha1 "github.com/kurama/komposed-sh/api/v1alpha1"
)

// KomposeManifestReconciler reconciles a KomposeManifest object
type KomposeManifestReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *KomposeManifestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the KomposeManifest instance
	var manifest komposev1alpha1.KomposeManifest
	if err := r.Get(ctx, req.NamespacedName, &manifest); err != nil {
		log.Error(err, "unable to fetch KomposeManifest")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Crée un répertoire temporaire pour Kompose
	tmpDir := filepath.Join(os.TempDir(), fmt.Sprintf("kompose-%s", time.Now().Format("20060102150405")))
	if err := os.MkdirAll(tmpDir, os.ModePerm); err != nil {
		return ctrl.Result{}, err
	}
	defer os.RemoveAll(tmpDir)

	// Écrire docker-compose.yaml dans tmpDir
	composePath := filepath.Join(tmpDir, "docker-compose.yaml")
	if err := os.WriteFile(composePath, []byte(manifest.Spec.DockerCompose), 0644); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to write compose file: %w", err)
	}

	// Kompose convert
	convertCmd := exec.Command("kompose", "convert", "-f", composePath, "-o", tmpDir)
	var convertOut, convertErr bytes.Buffer
	convertCmd.Stdout = &convertOut
	convertCmd.Stderr = &convertErr
	if err := convertCmd.Run(); err != nil {
		log.Error(err, "kompose convert failed", "stdout", convertOut.String(), "stderr", convertErr.String())
		return ctrl.Result{}, fmt.Errorf("kompose convert failed: %v - %s", err, convertErr.String())
	}

	// Kubectl apply
	files, err := os.ReadDir(tmpDir)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to read tmpDir: %w", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		name := file.Name()
		if name == "docker-compose.yaml" || !strings.HasSuffix(name, ".yaml") {
			continue
		}

		applyCmd := exec.Command("kubectl", "apply", "-f", filepath.Join(tmpDir, name), "-n", manifest.Namespace)
		var applyOut, applyErr bytes.Buffer
		applyCmd.Stdout = &applyOut
		applyCmd.Stderr = &applyErr
		if err := applyCmd.Run(); err != nil {
			log.Error(err, "kubectl apply failed", "file", name, "stdout", applyOut.String(), "stderr", applyErr.String())
			return ctrl.Result{}, fmt.Errorf("kubectl apply failed for file %s: %v - %s", name, err, applyErr.String())
		}
	}

	log.Info("Successfully reconciled KomposeManifest", "name", manifest.Name)
	return ctrl.Result{}, nil
}

func (r *KomposeManifestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&komposev1alpha1.KomposeManifest{}).
		Complete(r)
}
