package course

import "testing"

func TestDeadlinesOffTurnThePenaltyOff(t *testing.T) {
	if (Settings{TrackDeadlines: false, LatePenaltyEnabled: true, LatePenalty: 1}).Penalize() {
		t.Fatal("turning deadline tracking off must turn the penalty off")
	}

	if !(Settings{TrackDeadlines: true, LatePenaltyEnabled: true, LatePenalty: 1}).Penalize() {
		t.Fatal("the penalty applies when deadlines are tracked")
	}
}
