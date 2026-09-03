package api

import "testing"

func TestParseLogTail(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{name: "default", value: "", want: 200},
		{name: "minimum", value: "1", want: 1},
		{name: "selected", value: "500", want: 500},
		{name: "maximum", value: "5000", want: 5000},
		{name: "zero", value: "0", wantErr: true},
		{name: "negative", value: "-1", wantErr: true},
		{name: "over maximum", value: "5001", wantErr: true},
		{name: "not a number", value: "all", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseLogTail(test.value)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseLogTail(%q) error = %v, wantErr %v", test.value, err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("parseLogTail(%q) = %d, want %d", test.value, got, test.want)
			}
		})
	}
}
