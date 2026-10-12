package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/readium/go-toolkit/pkg/storage"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func newStorageFlagCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "serve"}
	cmd.Flags().StringVar(&storageModeFlag, "storage", string(storage.DefaultMode), "")
	cmd.Flags().StringVar(&localRootFlag, "local-root", "", "")
	cmd.Flags().StringVar(&s3BucketFlag, "s3-bucket", "", "")
	cmd.Flags().StringVar(&s3RegionFlag, "s3-region", "", "")
	cmd.Flags().StringVar(&s3EndpointFlag, "s3-endpoint", "", "")
	cmd.Flags().StringVar(&baseURLFlag, "base-url", "", "")
	return cmd
}

func TestResolveStorageConfigPrecedence(t *testing.T) {
	t.Setenv("STORAGE", "s3")
	t.Setenv("STORAGE_LOCAL_ROOT", "/env/root")

	cmd := newStorageFlagCommand()
	require.NoError(t, cmd.Flags().Set("storage", "local"))
	require.NoError(t, cmd.Flags().Set("local-root", "/flag/root"))

	cfg := resolveStorageConfig(cmd, []string{"/arg/root"})
	require.Equal(t, storage.ModeLocal, cfg.Mode)
	require.Equal(t, "/flag/root", cfg.LocalRoot)
}

func TestResolveStorageConfigEnvOverridesDefault(t *testing.T) {
	t.Setenv("STORAGE", "local")
	t.Setenv("STORAGE_LOCAL_ROOT", "/env/root")

	cmd := newStorageFlagCommand()
	cfg := resolveStorageConfig(cmd, []string{"/arg/root"})

	require.Equal(t, storage.ModeLocal, cfg.Mode)
	require.Equal(t, "/env/root", cfg.LocalRoot)
}

func TestResolveStorageConfigDefaultModeHTTP(t *testing.T) {
	cmd := newStorageFlagCommand()
	cfg := resolveStorageConfig(cmd, nil)
	require.Equal(t, storage.ModeHTTP, cfg.Mode)
}

func TestResolveStorageConfigArgFallbackForLocalRoot(t *testing.T) {
	cmd := newStorageFlagCommand()
	require.NoError(t, cmd.Flags().Set("storage", "local"))

	cfg := resolveStorageConfig(cmd, []string{"./books"})
	require.Equal(t, "books", cfg.LocalRoot)
}

func TestResolveStorageConfigFeedsFailFastValidation(t *testing.T) {
	cmd := newStorageFlagCommand()
	require.NoError(t, cmd.Flags().Set("storage", "http"))

	cfg := resolveStorageConfig(cmd, nil)
	_, err := storage.NewBackend(context.Background(), cfg)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "STORAGE_BASE_URL"))
}

func TestServeCommandHasStorageFlags(t *testing.T) {
	for _, name := range []string{"storage", "local-root", "s3-bucket", "s3-region", "s3-endpoint", "base-url"} {
		f := serveCmd.Flags().Lookup(name)
		require.NotNil(t, f, "missing flag %s", name)
	}
}
