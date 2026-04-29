package wecom

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSessionStorePersistsChatMapping(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "wecom-bot-state.json")

	store, err := loadSessionStore(path)
	require.NoError(t, err)

	_, ok := store.sessionID("single:alice")
	require.False(t, ok)

	err = store.setSessionID("single:alice", "session-1")
	require.NoError(t, err)

	reloaded, err := loadSessionStore(path)
	require.NoError(t, err)

	sessionID, ok := reloaded.sessionID("single:alice")
	require.True(t, ok)
	require.Equal(t, "session-1", sessionID)
}
