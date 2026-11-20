package endpoint

import (
	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/render"
	"testing"
)

func TestPhoneTabRowsSurviveSnapshot(t *testing.T) {
	s := model.New(t.TempDir(), 100, 35)
	s.PhoneTabRows = true
	var encoder SnapshotEncoder
	var decoder SnapshotDecoder
	data, err := encoder.Encode(render.Input{State: s})
	if err != nil {
		t.Fatal(err)
	}
	in, err := decoder.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if !in.State.PhoneTabRows || in.State.TabBarArea().H != 2 || in.State.PanesArea().Y != 2 {
		t.Fatal("geometry lost in snapshot")
	}
}
