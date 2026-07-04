package catalog

import (
	"reflect"
	"testing"
)

func TestParseIndex(t *testing.T) {
	cases := []struct {
		name string
		json string
		want []Meta
	}{
		{
			name: "object with skills, kind defaults to skill",
			json: `{"skills":[
				{"name":"greeter","description":"says hi","category":"chat","tags":["hello"]},
				{"kind":"plugin","name":"gitplug","description":"git tools","gitURL":"https://github.com/o/gitplug"}
			]}`,
			want: []Meta{
				{Kind: KindSkill, Name: "greeter", Description: "says hi", Category: "chat", Tags: []string{"hello"}},
				{Kind: KindPlugin, Name: "gitplug", Description: "git tools", GitURL: "https://github.com/o/gitplug"},
			},
		},
		{
			name: "extensions key",
			json: `{"extensions":[{"kind":"plugin","name":"p","description":"d"}]}`,
			want: []Meta{{Kind: KindPlugin, Name: "p", Description: "d"}},
		},
		{
			name: "bare array",
			json: `[{"name":"n","description":"d","repo":"o/r","path":"skills/n"}]`,
			want: []Meta{{Kind: KindSkill, Name: "n", Description: "d", Repo: "o/r", Path: "skills/n"}},
		},
		{
			name: "empty object",
			json: `{}`,
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseIndex([]byte(tc.json))
			if err != nil {
				t.Fatalf("ParseIndex: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseIndex\n got=%+v\nwant=%+v", got, tc.want)
			}
		})
	}
}

func TestParseIndex_Invalid(t *testing.T) {
	if _, err := ParseIndex([]byte("not json")); err == nil {
		t.Fatal("expected error on malformed json")
	}
}
