package config

import (
	"reflect"
	"testing"

	"mtun/pkg/proto"
)

func TestNormalizeVisibility(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		list     []string
		wantMode string
		wantList []string
		wantErr  bool
	}{
		{"默认所有人", "", nil, proto.VisibleAll, nil, false},
		{"仅自己", "self", []string{"x"}, proto.VisibleSelf, nil, false},
		{"指定节点去重", "nodes", []string{" b ", "a", "b", ""}, proto.VisibleNodes, []string{"b", "a"}, false},
		{"指定节点为空", "nodes", nil, "", nil, true},
		{"未知模式", "friends", nil, "", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, list, err := NormalizeVisibility(tc.input, tc.list)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if mode != tc.wantMode || !reflect.DeepEqual(list, tc.wantList) {
				t.Fatalf("got (%q, %#v), want (%q, %#v)", mode, list, tc.wantMode, tc.wantList)
			}
		})
	}
}
