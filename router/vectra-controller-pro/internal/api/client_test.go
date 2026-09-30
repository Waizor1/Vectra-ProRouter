package api

import "testing"

func TestParseSingleStat_JSON(t *testing.T) {
	out := []byte(`{"stat":{"name":"outbound>>>node-world>>>traffic>>>uplink","value":"12345"}}`)
	s, err := parseSingleStat(out, "outbound>>>node-world>>>traffic>>>uplink")
	if err != nil {
		t.Fatal(err)
	}
	if s.Value != 12345 {
		t.Fatalf("got %d", s.Value)
	}
}

func TestParseSingleStat_PlainText(t *testing.T) {
	out := []byte("outbound>>>node-world>>>traffic>>>uplink 99")
	s, err := parseSingleStat(out, "outbound>>>node-world>>>traffic>>>uplink")
	if err != nil {
		t.Fatal(err)
	}
	if s.Value != 99 {
		t.Fatalf("got %d", s.Value)
	}
}

func TestParseStatList_JSON(t *testing.T) {
	out := []byte(`{"stat":[{"name":"a","value":1},{"name":"b","value":"2"}]}`)
	list, err := parseStatList(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Value != 1 || list[1].Value != 2 {
		t.Fatalf("got %+v", list)
	}
}
