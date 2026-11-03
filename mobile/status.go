// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/muelli/synctang/mrcore/transport"
	"github.com/muelli/syncthing-socket/discovery"
	"github.com/syncthing/syncthing/lib/protocol"
)

// What discovery says about a machine, for a phone to show a person before they walk
// to it. The strings are the contract with the app.
const (
	// StatusWaiting: the machine announced itself within WaitingRecordMaxAge, which is
	// what a machine at its unlock prompt does every minute.
	StatusWaiting = "WAITING"
	// StatusStale: there is a record, but an old one, or one with no usable timestamp.
	// The machine announced at some point and may not be at its prompt now.
	StatusStale = "STALE"
	// StatusAbsent: discovery has nothing for the machine. It may still be starting, be
	// switched off, or use another discovery server.
	StatusAbsent = "ABSENT"
	// StatusNoNetwork: the lookup itself failed, so nothing can be said about the machine.
	StatusNoNetwork = "NO_NETWORK"
)

// statusTimeout bounds one check, on top of the lookup's own limit.
const statusTimeout = 15 * time.Second

// MachineStatus is the answer, shaped for gomobile: no time.Time, no slices.
// AgeSeconds is how long ago the machine announced, or -1 when there is no record or no
// usable timestamp.
type MachineStatus struct {
	State      string
	AgeSeconds int
}

// CheckMachineStatus asks the discovery server (empty means the public one; the URL may
// carry ?id= or ?insecure, exactly as for a dial) what it knows of deviceID and says
// whether the machine looks like it is waiting. One HTTPS GET and no connection to the
// machine, which serves one session and would have it used up by a probe.
//
// "Waiting" means a record young enough, not that an unlock will work: a record can be
// fresh and the machine's relay gone a moment later. An error is only for input that
// cannot be checked (an empty or malformed device ID, a discovery URL that is not valid);
// a lookup that fails is the state NO_NETWORK.
func CheckMachineStatus(deviceID, discoveryURL string) (*MachineStatus, error) {
	return checkMachineStatusAt(time.Now, deviceID, discoveryURL)
}

func checkMachineStatusAt(now func() time.Time, deviceID, discoveryURL string) (*MachineStatus, error) {
	if deviceID == "" {
		return nil, errors.New("mobile: no machine device ID to check")
	}
	id, err := protocol.DeviceIDFromString(deviceID)
	if err != nil {
		return nil, fmt.Errorf("mobile: the machine device ID is not valid: %w", err)
	}
	cfg, err := transport.ParseDialerNetworkConfig(discoveryURL, "")
	if err != nil {
		return nil, fmt.Errorf("mobile: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()
	record, err := transport.LookupRecord(ctx, id.String(), cfg.Discovery)
	if err != nil {
		logEvent(levelTrace, "machine status", "machine", deviceID, "state", StatusNoNetwork, "error", err)
		return &MachineStatus{State: StatusNoNetwork, AgeSeconds: -1}, nil
	}
	status := statusOf(record != nil, record, now())
	logEvent(levelTrace, "machine status", "machine", deviceID, "state", status.State, "age_s", status.AgeSeconds)
	return status, nil
}

// statusOf is the decision, apart from the network: found says whether there was a record.
func statusOf(found bool, record *discovery.Record, now time.Time) *MachineStatus {
	if !found {
		return &MachineStatus{State: StatusAbsent, AgeSeconds: -1}
	}
	if record.Seen.IsZero() {
		return &MachineStatus{State: StatusStale, AgeSeconds: -1}
	}
	age := now.Sub(record.Seen)
	if age < 0 {
		age = 0 // a clock that is ahead of this phone's, or a bad timestamp: as good as now
	}
	state := StatusStale
	if age <= transport.WaitingRecordMaxAge {
		state = StatusWaiting
	}
	return &MachineStatus{State: state, AgeSeconds: int(age.Seconds())}
}
