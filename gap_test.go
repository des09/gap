package main

import (
	"bytes"
	"fmt"
	"github.com/stretchr/testify/assert"
	"regexp"
	"testing"
)

type ht struct {
	h holder
	t string
}

func TestGetTCPMap(t *testing.T) {
	r := map[string]string{}

	getTCPMap("spec/proc/net/tcp", r)
	assert.NotEmpty(t, r)
	assert.Equal(t, 6, len(r))
	assert.Equal(t, "18EB", r["21620"])
	fmt.Print(r)

	getTCPMap("spec/proc/net/tcp6", r)
	assert.NotEmpty(t, r)
	assert.Equal(t, 6, len(r))
	assert.Equal(t, "18EB", r["21620"])
	fmt.Print(r)
}

func TestShortPipe(t *testing.T) {
	proc = "spec/proc"

	m := map[string]string{"21620": "18EB"}
	buf := new(bytes.Buffer)
	emitBare(mapPorts(m, getSockets(findPids())), buf)
	s := buf.String()
	assert.Contains(t, s, "1446\t6379")
	assert.Regexp(t, "^[0-9]+\\s[0-9]+\\s*\\n$", s)

}

func TestEmitBare(t *testing.T) {

	tcs := []ht{
		{h: holder{pid: "1446", port: int64(6379)}, t: "1446\t6379\t\t\n"},
		{h: holder{pid: "1446", port: int64(6379), ports: "ports"}, t: "1446\tports\t\t\n"},
		{h: holder{pid: "1446", port: int64(6379), cmd: []byte("cmd ")}, t: "1446\t6379\t\tcmd \n"},
	}

	for _, tc := range tcs {
		buf := new(bytes.Buffer)
		emitBare(from(tc.h), buf)

		s := buf.String()
		assert.Equal(t, tc.t, s)
	}

}

func TestEmitFormatted(t *testing.T) {
	tcs := []ht{
		{h: holder{pid: "1446", port: int64(6379)}, t: "pid   port\n1446  6379  \n"},
		{h: holder{pid: "1446", port: int64(6379), ports: "ports"}, t: "pid   port\n1446  ports  \n"},
		{h: holder{pid: "1446", port: int64(6379), cmd: []byte("cmd "), cmdColor: 22}, t: "pid   port\n1446  6379  cmd \n"},
	}

	for _, tc := range tcs {
		buf := new(bytes.Buffer)
		emitFormatted(from(tc.h), buf)

		s := buf.String()
		assert.Equal(t, tc.t, s)
	}

}

func from(vi ...holder) chan holder {
	in := make(chan holder)
	go func() {
		for _, v := range vi {
			in <- v
		}
		close(in)
	}()
	return in
}

func TestMapCommands(t *testing.T) {
	proc = "spec/proc"
	in := make(chan holder)
	out := mapCommands(in)

	in <- holder{pid: "1446"}
	po := <-out
	assert.Regexp(t, "/usr/bin/redis-server.*", string(po.cmd))

	in <- holder{pid: "not-there"}
	po = <-out
	assert.Equal(t, "", string(po.cmd))

	close(in)
	_, bad := <-out
	assert.False(t, bad)
}

func TestGather(t *testing.T) {
	in := make(chan holder)
	out := gather(in)
	m := make(map[string]holder)

	in <- holder{pid: "1446", port: int64(12)}
	in <- holder{pid: "1446", port: int64(21)}
	in <- holder{pid: "1446", port: int64(2)}
	in <- holder{pid: "1447", port: int64(3)}

	close(in)

	for o := range out {
		m[o.pid] = o
	}

	assert.Len(t, m, 2)
	assert.Equal(t, "2,12,21", m["1446"].ports)
	assert.Equal(t, int64(2), m["1446"].port)
	assert.Equal(t, "3", m["1447"].ports)
}

func TestSortJ(t *testing.T) {
	in1 := holder{pid: "1000", port: int64(100), cmd: []byte("a cmd")}
	in2 := holder{pid: "2000", port: int64(200), cmd: []byte("b cmd")}
	in3 := holder{pid: "3000", port: int64(300), cmd: []byte("c cmd")}
	in11 := holder{pid: "1000", port: int64(100), cmd: []byte("a cmd")}

	test := func(si int, f ...holder) {
		in := make(chan holder)
		out := sortJ(in, si)
		for _, fi := range f {
			in <- fi
		}
		close(in)

		assert.Equal(t, in1, <-out)
		assert.Equal(t, in2, <-out)
		assert.Equal(t, in3, <-out)
		_, bad := <-out
		assert.False(t, bad)
	}

	test(0, in1, in3, in2)
	test(0, in2, in3, in1)
	test(1, in1, in3, in2)
	test(2, in1, in3, in2)

	test2 := func(si int, f ...holder) {
		in := make(chan holder)
		out := sortJ(in, si)
		for _, fi := range f {
			in <- fi
		}
		close(in)
		_, ok := <-out
		assert.True(t, ok)
		_, ok = <-out
		assert.True(t, ok)
		_, bad := <-out
		assert.False(t, bad)
	}
	test2(0, in1, in11)
	test2(1, in1, in11)
	test2(2, in1, in11)

}

func TestGreps(t *testing.T) {
	in := []holder{
		{cmd: []byte("foo")},
		{},
		{cmd: []byte("bar")},
	}

	out := grep(regexp.MustCompile(".o."), from(in...))

	po, ok := <-out
	assert.True(t, ok)
	assert.Equal(t, "foo", string(po.cmd))

	po, extra := <-out
	assert.False(t, extra)
}
