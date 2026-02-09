package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateChameleonSessionKey(t *testing.T) {
	t.Parallel()

	valid := "agent:chameleon:oc:a1b2c3d4-5678-90ab-cdef-1234567890ab"
	assert.True(t, ValidateChameleonSessionKey(valid))
	assert.False(t, ValidateChameleonSessionKey("agent:main:slack"))
	assert.False(t, ValidateChameleonSessionKey("agent:chameleon:oc:not-a-uuid"))
	assert.False(t, ValidateChameleonSessionKey("agent:chameleon:oc:"))
}

func TestExtractChameleonSessionAgentID(t *testing.T) {
	t.Parallel()

	key := "agent:chameleon:oc:A1B2C3D4-5678-90AB-CDEF-1234567890AB"
	agentID, ok := ExtractChameleonSessionAgentID(key)
	require.True(t, ok)
	assert.Equal(t, "a1b2c3d4-5678-90ab-cdef-1234567890ab", agentID)

	agentID, ok = ExtractChameleonSessionAgentID("agent:main:slack")
	assert.False(t, ok)
	assert.Equal(t, "", agentID)
}

func TestExtractSessionAgentIdentity(t *testing.T) {
	t.Parallel()

	canonical := "agent:chameleon:oc:a1b2c3d4-5678-90ab-cdef-1234567890ab"
	assert.Equal(t, "a1b2c3d4-5678-90ab-cdef-1234567890ab", ExtractSessionAgentIdentity(canonical))
	assert.Equal(t, "main", ExtractSessionAgentIdentity("agent:main:slack:channel:C123"))
	assert.Equal(t, "2b", ExtractSessionAgentIdentity("agent:2b:main"))
	assert.Equal(t, "", ExtractSessionAgentIdentity("session:main"))
}
