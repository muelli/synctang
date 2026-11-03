// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/muelli/syncthing-socket/discovery"
)

// What discovery says about a machine. The strings are the contract with the phone app
// (they are what mobile.CheckMachineStatus returns).
const (
	// StatusWaiting: the machine announced itself within WaitingRecordMaxAge, which is
	// what a machine at its unlock prompt does every minute.
	StatusWaiting = "WAITING"
	// StatusStale: there is a record, but an old one, or one with no usable timestamp.
	StatusStale = "STALE"
	// StatusAbsent: discovery has nothing for the machine.
	StatusAbsent = "ABSENT"
	// StatusNoNetwork: the lookup itself failed, so nothing can be said about the machine.
	StatusNoNetwork = "NO_NETWORK"
	// StatusRateLimited: the discovery server answered HTTP 429. That says nothing about the
	// machine or the network; RetryAfter is how long it asked to be left alone.
	StatusRateLimited = "RATE_LIMITED"
)

// MachineStatus is what one discovery lookup says about a machine. Age is how long ago the
// machine announced, or -1 when there is no record or no usable timestamp. RetryAfter is only
// set for StatusRateLimited, as the server said it (not bounded).
type MachineStatus struct {
	State      string
	Age        time.Duration
	RetryAfter time.Duration
}

// StatusOf is the decision, apart from the network: found says whether there was a record.
func StatusOf(found bool, record *discovery.Record, now time.Time) MachineStatus {
	if !found {
		return MachineStatus{State: StatusAbsent, Age: -1}
	}
	if record.Seen.IsZero() {
		return MachineStatus{State: StatusStale, Age: -1}
	}
	age := now.Sub(record.Seen)
	if age < 0 {
		age = 0 // a clock that is ahead of ours, or a bad timestamp: as good as now
	}
	state := StatusStale
	if age <= WaitingRecordMaxAge {
		state = StatusWaiting
	}
	return MachineStatus{State: state, Age: age}
}

// CheckMachine does one lookup of deviceID on the discovery server at discoveryURL (empty
// means the public one) and says what it found. Never an error: a lookup that fails is
// StatusNoNetwork, one that is answered with a 429 is StatusRateLimited. It does not connect
// to the machine, which serves one session and would have it used up by a probe.
func CheckMachine(ctx context.Context, deviceID, discoveryURL string, now func() time.Time) MachineStatus {
	record, err := LookupRecord(ctx, deviceID, discoveryURL)
	var limited *RateLimitedError
	switch {
	case errors.As(err, &limited):
		return MachineStatus{State: StatusRateLimited, Age: -1, RetryAfter: limited.RetryAfter}
	case err != nil:
		return MachineStatus{State: StatusNoNetwork, Age: -1}
	}
	return StatusOf(record != nil, record, now())
}

// FormatAge is an age for a person: whole seconds under a minute, minutes under two hours,
// hours under two days, days after that ("42 s", "47 min", "5 h", "3 days").
func FormatAge(age time.Duration) string {
	secs := int64(max(age, 0) / time.Second)
	switch {
	case secs < 60:
		return fmt.Sprintf("%d s", secs)
	case secs < 120*60:
		return fmt.Sprintf("%d min", secs/60)
	case secs < 48*3600:
		return fmt.Sprintf("%d h", secs/3600)
	default:
		return fmt.Sprintf("%d days", secs/86400)
	}
}

// DialFailureHint is what to tell a person whose dial of the machine gave up, from what
// discovery says about it (see CheckMachine): the machine announced itself and did not
// answer, or announced itself long ago, or never did. Empty when discovery does not tell
// anything (no network, rate limited, a state this does not know), in which case the caller
// keeps its own general text. The phone app has the same texts in its strings.xml.
func DialFailureHint(st MachineStatus) string {
	switch st.State {
	case StatusWaiting:
		return fmt.Sprintf("The machine announced itself %s ago but did not answer. Try again. If it keeps failing, check its network.", FormatAge(st.Age))
	case StatusStale:
		if st.Age < 0 {
			return "The machine announced itself at some point, so it may not be at its prompt. Check the machine."
		}
		return fmt.Sprintf("The machine last announced itself %s ago, so it may not be at its prompt. Check the machine.", FormatAge(st.Age))
	case StatusAbsent:
		return "The machine has not announced itself. It may still be starting, switched off, or using another discovery server."
	}
	return ""
}
