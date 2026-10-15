package infrastructure

import (
	"reflect"
	"testing"

	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
)

func TestParseHelperEntries(t *testing.T) {
	out := "credential.helper\nosxkeychain\x00credential.https://github.com/.helper\n\x00credential.https://github.com/.helper\n!\"/x/gitswitch\" credential\x00credential.helper\x00credential.usehttppath\ntrue\x00"
	want := []hosting.HelperEntry{{URL: "", Value: "osxkeychain"}, {URL: "https://github.com/", Value: ""}, {URL: "https://github.com/", Value: `!"/x/gitswitch" credential`}, {URL: "", Value: ""}}
	if got := ParseHelperEntries(out); !reflect.DeepEqual(got, want) {
		t.Errorf("ParseHelperEntries = %q\nwant %q", got, want)
	}
}
