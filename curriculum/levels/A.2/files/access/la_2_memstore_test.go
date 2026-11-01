package access_test

import (
	"testing"

	"burrow/access"
	"burrow/access/accesstest"
)

func TestLA_2_MemStoreConformance(t *testing.T) {
	var _ access.TeamStore = access.NewMemStore()
	accesstest.RunTeamStore(t, func(t *testing.T) access.TeamStore {
		return access.NewMemStore()
	})
}
