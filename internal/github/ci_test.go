package github

import "testing"

func TestSettle(t *testing.T) {
	var (
		pass          = Check{Status: "COMPLETED", Conclusion: "SUCCESS"}
		skip          = Check{Status: "COMPLETED", Conclusion: "SKIPPED"}
		fail          = Check{Status: "COMPLETED", Conclusion: "FAILURE"}
		timeout       = Check{Status: "COMPLETED", Conclusion: "TIMED_OUT"}
		running       = Check{Status: "IN_PROGRESS"}
		queued        = Check{Status: "QUEUED"}
		statusOK      = Check{State: "SUCCESS"}
		statusPending = Check{State: "PENDING"}
		statusError   = Check{State: "ERROR"}
	)
	cases := []struct {
		name            string
		checks          []Check
		settled, failed bool
	}{
		{"no checks yet", nil, false, false},
		{"all green", []Check{pass, skip, statusOK}, true, false},
		{"a failure", []Check{pass, fail}, true, true},
		{"a timeout", []Check{timeout}, true, true},
		{"a failed commit status", []Check{pass, statusError}, true, true},
		{"a failure while another runs", []Check{fail, running}, false, true},
		{"queued", []Check{pass, queued}, false, false},
		{"a pending commit status", []Check{fail, statusPending}, false, true},
	}
	for _, tc := range cases {
		settled, failed := Settle(tc.checks)
		if settled != tc.settled || failed != tc.failed {
			t.Errorf("%s: Settle() = settled %v, failed %v; want %v, %v", tc.name, settled, failed, tc.settled, tc.failed)
		}
	}
}
