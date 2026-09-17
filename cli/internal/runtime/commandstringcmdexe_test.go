package runtime

import "testing"

func TestWrapForCmdExeMetacharactersWrapsUnquotedAmpersand(t *testing.T) {
	got := wrapForCmdExeMetacharacters(`C:\App&Co\bifrost.exe auth print-token`)
	want := `"C:\App&Co\bifrost.exe auth print-token"`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestWrapForCmdExeMetacharactersLeavesSafeCommandUnchanged(t *testing.T) {
	command := `"C:\Program Files\bifrost\bifrost.exe" auth print-token`
	if got := wrapForCmdExeMetacharacters(command); got != command {
		t.Fatalf("got %q, want unchanged %q", got, command)
	}
}

func TestWrapForCmdExeMetacharactersCoversAllMetacharacters(t *testing.T) {
	for _, metacharacter := range []string{"&", "|", "<", ">", "^"} {
		command := "C:\\bifrost" + metacharacter + "co\\bifrost.exe"
		got := wrapForCmdExeMetacharacters(command)
		want := `"` + command + `"`
		if got != want {
			t.Fatalf("metacharacter %q: got %q, want %q", metacharacter, got, want)
		}
	}
}
