package main

import "testing"
import (
	"bytes"
	"fmt"
	"github.com/stretchr/testify/assert"
	"regexp"
	"time"
)

func TestSomething(t *testing.T) {
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
	emit(mapPorts(m, getSockets(findPids())), buf)
	s := buf.String()
	assert.Contains(t, s, "1446\t6379")
	assert.Regexp(t, "^[0-9]+\\s[0-9]+\\s*\\n$", s)

}

func TestCommands(t *testing.T) {
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
	for range out {
		assert.Fail(t, "out should be closed")
	}
}

func TestGather(t *testing.T) {
	in := make(chan holder)
	out := gather(in)
	m := make(map[string]holder)

	in <- holder{pid: "1446", port: int64(1)}
	in <- holder{pid: "1446", port: int64(2)}
	in <- holder{pid: "1447", port: int64(3)}

	close(in)

	for o := range out {
		m[o.pid] = o
	}

	assert.Len(t, m, 2)
	assert.Equal(t, "1,2", m["1446"].ports)
	assert.Equal(t, "3", m["1447"].ports)
}

func TestGreps(t *testing.T) {
	in := make(chan holder)
	out := grep(regexp.MustCompile(".o."), in)

	in <- holder{cmd: []byte("foo")}
	in <- holder{}
	in <- holder{cmd: []byte("bar")}

	po, to := getOrTimeout(out, time.Millisecond*100)
	assert.False(t, to)
	assert.Equal(t, "foo", string(po.cmd))

	po, to = getOrTimeout(out, time.Millisecond*10)
	assert.True(t, to)

	close(in)

	for range out {
		assert.Fail(t, "out should be closed")
	}
}

func getOrTimeout(c chan holder, d time.Duration) (ret holder, timedOut bool) {
	select {
	case res := <-c:
		return res, false
	case <-time.After(d):
		fmt.Printf("timeout ")
		return holder{}, true
	}
}
