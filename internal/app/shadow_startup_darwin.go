//go:build darwin

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	buildsetmodel "github.com/zanescope/v-local-cli/internal/shadowbuildset"
	clockmodel "github.com/zanescope/v-local-cli/internal/shadowclock"
	keychainmodel "github.com/zanescope/v-local-cli/internal/shadowkeychain"
	publishmodel "github.com/zanescope/v-local-cli/internal/shadowpublish"
	"github.com/zanescope/v-local-cli/internal/state"
)

type lazyStartupKeychain struct {
	once    sync.Once
	backend *keychainmodel.HelperKeychain
	err     error
}

func (value *lazyStartupKeychain) initialize() (*keychainmodel.HelperKeychain, error) {
	value.once.Do(func() {
		executable, err := os.Executable()
		if err != nil {
			value.err = errors.New("Shadow startup executable identity is unavailable")
			return
		}
		executable, err = filepath.EvalSymlinks(executable)
		if err != nil || filepath.Clean(executable) != executable || filepath.Base(executable) != "v-local-cli" {
			value.err = errors.New("Shadow startup is not running from a canonical CLI")
			return
		}
		manifest, _, err := buildsetmodel.Load(filepath.Dir(executable))
		if err != nil || manifest.RouteMode != buildsetmodel.RouteProductionCapable {
			value.err = errors.New("Shadow startup frozen production build set is unavailable")
			return
		}
		expectedDigest := ""
		for _, artifact := range manifest.Artifacts {
			if artifact.Role == "cli" && artifact.Leaf == filepath.Base(executable) {
				expectedDigest = artifact.SHA256
				break
			}
		}
		if expectedDigest == "" {
			value.err = errors.New("Shadow startup build set lacks the CLI helper binding")
			return
		}
		value.backend, value.err = keychainmodel.NewHelperKeychain(executable, expectedDigest)
	})
	if value.err != nil || value.backend == nil {
		return nil, errors.New("Shadow startup Keychain helper is unavailable")
	}
	return value.backend, nil
}

func (value *lazyStartupKeychain) Put(ctx context.Context, accountID, generationID string, payload []byte) error {
	backend, err := value.initialize()
	if err != nil {
		return err
	}
	return backend.Put(ctx, accountID, generationID, payload)
}

func (value *lazyStartupKeychain) Get(ctx context.Context, accountID, generationID string) ([]byte, bool, error) {
	backend, err := value.initialize()
	if err != nil {
		return nil, false, err
	}
	return backend.Get(ctx, accountID, generationID)
}

func (value *lazyStartupKeychain) Delete(ctx context.Context, accountID, generationID string) error {
	backend, err := value.initialize()
	if err != nil {
		return err
	}
	return backend.Delete(ctx, accountID, generationID)
}

func newPlatformStartupGenerationReconciler(accountID string) (startupGenerationReconciler, error) {
	root, err := state.AccountDir(accountID)
	if err != nil {
		return nil, err
	}
	stateStore, err := publishmodel.NewFileStateStore(root, accountID)
	if err != nil {
		return nil, err
	}
	locker, err := publishmodel.NewFileLocker(root, accountID)
	if err != nil {
		return nil, err
	}
	return &publishmodel.Publisher{
		Clock: clockmodel.System{}, State: stateStore, Keychain: &lazyStartupKeychain{}, Locker: locker,
	}, nil
}

func reconcilePlatformShadowGenerations(ctx context.Context) error {
	accounts, err := state.List()
	if err != nil {
		return err
	}
	return reconcileStartupShadowGenerations(
		ctx, clockmodel.System{}, accounts, newPlatformStartupGenerationReconciler,
	)
}
