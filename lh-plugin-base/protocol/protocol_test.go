package protocol

import (
	"encoding/json"
	"testing"
)

func TestDecodeIsStrict(t *testing.T) {
	var request ReadQueryRequest
	if err := Decode(json.RawMessage(`{"query":"status"}`), &request); err != nil {
		t.Fatal(err)
	}
	if request.Query != "status" {
		t.Fatalf("query = %q", request.Query)
	}
	if err := Decode(json.RawMessage(`{"query":"status","extra":true}`), &request); err == nil {
		t.Fatal("expected unknown field to fail")
	}
	if err := Decode(json.RawMessage(`{"query":"status"} {}`), &request); err == nil {
		t.Fatal("expected trailing value to fail")
	}
}

func TestCapabilityResultsUseCanonicalErrorShape(t *testing.T) {
	success := Success(ReadQueryResult{Summary: "ok", Facts: []ReadFact{}})
	if success.Error != nil || !json.Valid(success.Result) {
		t.Fatalf("success = %+v", success)
	}
	failure := Failure(ErrorCapabilityUnsupported, "unsupported capability")
	if failure.Error == nil || failure.Error.Code != "capability_not_supported" || failure.Result != nil {
		t.Fatalf("failure = %+v", failure)
	}
}
