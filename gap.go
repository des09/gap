package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"bufio"
	flag "github.com/spf13/pflag"
	"io"
	"regexp"
	"strconv"
	"sync"
	"text/tabwriter"

	"github.com/reconquest/loreley"
	"sort"
)

type holder struct {
	pid      string
	inode    string
	port     int64
	ports    string
	cmd      []byte
	cmdColor int
}
type alias struct {
	display []byte
	regexp  *regexp.Regexp
	color   int
}

var once sync.Once
var proc = "/proc"
var aliases = []alias{
	{[]byte("webstorm"), regexp.MustCompile("java.*webstorm"), 51},
	{[]byte("@1"), regexp.MustCompile(`^/usr/bin/([\w_\-\.]+)\s.*`), 48},
	{[]byte("@1"), regexp.MustCompile(`^/usr/sbin/([\w_\-\.]+)\s.*`), 46},
}

//findPids returns a channel of FileInfo matching the pattern /proc/[pid]/
// these are the directories containing process information in linux
func findPids() chan os.FileInfo {
	out := make(chan os.FileInfo)
	go func() {
		defer close(out)
		if d, err := os.OpenFile(proc, os.O_RDONLY, 000); err != nil {
			panic(err) //bail if we can't read the /proc dir
		} else {
			fi, err := d.Readdir(-1)
			handle(err)

			for _, p := range fi {
				if isInt(p.Name()) {
					out <- p
				}
			}
		}
	}()
	return out
}

//handle is for errors that we shouldn't stop for, we inform the user about
// the first, then let the rest slide
func handle(err error) {
	if err != nil {
		if strings.HasSuffix(err.Error(), "permission denied") {
			once.Do(func() {
				fmt.Fprintf(os.Stderr, "Permission denied, try running as root.\n")
			})
		} else {
			fmt.Fprintf(os.Stderr, "Error %v\n", err)
		}
	}
}

//getSockets is a pipeline operation that reads fileInfo from in that represents a /proc/[pid]/ directory.
// we read all the files in /proc/pid/fd, and for all the ones that represent sockets, we
// create a holder, and pass it to the out channel
func getSockets(in <-chan os.FileInfo) chan holder {

	out := make(chan holder)
	go func() {
		for n := range in {
			p := proc + "/" + n.Name() + "/fd"

			if f, e := os.Open(p); e != nil {
				handle(e)
			} else {

				if l, e := f.Readdir(-1); e != nil {
					handle(e)
				} else {
					for _, s := range l {

						if ss, b := socketFromLink(p + "/" + s.Name()); b {
							out <- holder{pid: n.Name(), inode: ss}
						}
					}
				}
			}
		}
		close(out)
	}()
	return out
}

func isInt(s string) bool {
	for _, c := range s {
		if c < 48 || c > 57 {
			return false
		}
	}
	return true
}

//mapPorts is a pipeline that fills in the port in the holder
func mapPorts(portMap map[string]string, in <-chan holder) chan holder {
	out := make(chan holder)
	go func() {
		for n := range in {
			if p, t := portMap[n.inode]; t {
				n.port, _ = strconv.ParseInt(strings.ToLower(p), 16, 32)
				out <- n
			}
		}
		close(out)
	}()
	return out
}

//mapCommands is a pipeline that fills in the commandline in the holder, by reading /proc/[]pid]/cmdline
func mapCommands(in <-chan holder) chan holder {
	out := make(chan holder)
	go func() {
		for n := range in {
			var err error
			n.cmd, err = ioutil.ReadFile(fmt.Sprintf("%s/%s/cmdline", proc, n.pid))
			if err != nil {
				handle(err)
			}
			//cmdline uses null bytes as separator, convert to spaces
			n.cmd = bytes.Replace(n.cmd, []byte{0}, []byte(" "), -1)
			out <- n
		}
		close(out)
	}()
	return out
}

//grep is a pipeline filter that drops holders when cmdline doesn't match regexp
func grep(regexp *regexp.Regexp, in <-chan holder) chan holder {
	out := make(chan holder, 12)
	go func() {
		for n := range in {
			if regexp.Match(n.cmd) {
				out <- n
			}
		}
		close(out)
	}()
	return out
}

//gather is a pipeline that reads all elements from the in pipe, groups them by pid,
// and passes one holder per pid to out channel, with ports set. Maintains the holder.port of the lowest in group, to preserve sort
func gather(in <-chan holder) chan holder {
	out := make(chan holder)
	m := make(map[string]holder)

	go func() {
		for n := range in {
			if v, t := m[n.pid]; t {
				if n.port > v.port {
					v.ports = v.ports + "," + strconv.FormatInt(n.port, 10)
				} else {
					v.port = n.port
					v.ports = strconv.FormatInt(n.port, 10) + "," + v.ports
				}
				m[n.pid] = v

			} else {
				n.ports = strconv.FormatInt(n.port, 10)
				m[n.pid] = n

			}
		}
		for _, v := range m {
			out <- v
		}
		close(out)
	}()
	return out
}

//sort is a pipeline that reads all elements from the in pipe, sorts them, then emits to out channel
// the index is 0 = pid, 1 = port, 2 = cmd
// the code assumes that ports and pids are smaller than 2^32 / 1000, so we take a small chance there... worst case sort order is off.
func sortJ(in <-chan holder, index int) chan holder {
	out := make(chan holder)
	ms := make(map[string]holder)
	mi := make(map[int]holder)

	uniqi := func(i int) int {
		ini := func(j int) bool {
			_, ok := mi[j]
			return ok
		}
		i = 1000 * i
		for ini(i) {
			i++
		}
		return i
	}

	uniqs := func(s string) string {
		ini := func(j string) bool {
			_, ok := ms[j]
			return ok
		}
		for ini(s) {
			s = s + "a"
		}
		return s
	}

	go func() {
		for n := range in {
			switch index {
			case 0:
				k, _ := strconv.Atoi(n.pid)
				mi[uniqi(k)] = n
			case 1:
				i := int(n.port)
				mi[uniqi(i)] = n
			case 2:
				k := string(n.cmd)
				ms[uniqs(k)] = n
			}
		}

		if len(ms) > 0 {
			var keys []string
			for k := range ms {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				out <- ms[k]
			}
		} else {
			var keys []int
			for k := range mi {
				keys = append(keys, k)
			}
			sort.Ints(keys)
			for _, k := range keys {
				out <- mi[k]
			}
		}
		close(out)
	}()
	return out
}

func socketFromLink(p string) (string, bool) {
	if s, e := os.Readlink(p); e == nil {
		if strings.HasPrefix(s, "socket:[") {
			s = s[8 : len(s)-1]
			return s, true
		}
	}
	return "", false

}

//emitBare terminates the pipeline and writes a tab separated line terminated
// with \\n to buf per holder received on the in channel
func emitBare(in <-chan holder, buf io.Writer) {
	for p := range in {
		if p.ports == "" {
			p.ports = strconv.FormatInt(p.port, 10)
		}
		fmt.Fprintf(buf, "%s\t%s\t\t%s\n", p.pid, p.ports, p.cmd)
	}
}

//emitFormatted terminates the pipeline and writes a colorized and formatted
// table to buf
func emitFormatted(in <-chan holder, buf io.Writer) {

	buffer := &bytes.Buffer{}

	writer := tabwriter.NewWriter(buffer, 2, 4, 2, ' ', tabwriter.FilterHTML)

	writer.Write([]byte(strings.Join(
		[]string{
			"<underline>pid<reset>",
			"<underline>port<reset>\n",
		}, "\t",
	)))

	for p := range in {
		if p.ports == "" {
			p.ports = strconv.FormatInt(p.port, 10)
		}
		if p.cmdColor > 0 {
			p.cmd = []byte(fmt.Sprintf("<fg %d>%s", p.cmdColor, p.cmd))
		}
		writer.Write([]byte(strings.Join(
			[]string{
				"<fg 15>" + p.pid + "<reset>",
				"<fg 58>" + p.ports + "<reset>",
				string(p.cmd) + "<reset>",
			}, "\t",
		)))
		writer.Write([]byte("\n"))
	}

	writer.Flush()

	loreley.DelimLeft = "<"
	loreley.DelimRight = ">"
	result, err := loreley.CompileAndExecuteToString(
		buffer.String(),
		nil,
		nil,
	)
	if err != nil {
		panic(err)
	}

	fmt.Fprint(buf, result)

}

//mapAliases replaces cmd with the display from the first alias that matches cmd
// and sets holder.cmdColor to the alias.cmdColor value. evaluates submatch replacement.
func mapAliases(in chan holder) chan holder {
	out := make(chan holder)
	go func() {
		for n := range in {
			for _, r := range aliases {
				if r.regexp.Match(n.cmd) {
					result := r.regexp.FindStringSubmatch(string(n.cmd))
					v := r.display
					for i, el := range result {
						t := []byte(fmt.Sprintf("@%d", i))
						v = bytes.Replace(v, t, []byte(el), -1)
					}

					n.cmd = v
					n.cmdColor = r.color
				}
			}
			out <- n
		}
		close(out)
	}()
	return out
}

func main() {

	commands := flag.BoolP("command", "c", false, "show commandlines")
	bare := flag.BoolP("bare", "b", false, "clean output, ")
	fsort := flag.IntP("sort", "s", -1, "sort, defaults to port if set with no value")
	flag.Lookup("sort").NoOptDefVal = "1"
	aliases := flag.BoolP("aliases", "a", false, "show aliases instead of full command")
	grepStr := flag.StringP("grep", "g", "", "grep for command")
	//kill := flag.Int("kill", false, "list commands")
	tableOutput := flag.BoolP("table", "t", false, "output one line per port")

	flag.Parse()

	m := make(map[string]string)
	err := getTCPMap("/proc/net/tcp", m)
	err = getTCPMap("/proc/net/tcp6", m)
	if err != nil {
		handle(err)
		return
	}

	pipe := mapPorts(m, getSockets(findPids()))

	if !*tableOutput {
		pipe = gather(pipe)
	}

	if *grepStr != "" {
		pipe = grep(regexp.MustCompile(*grepStr), mapCommands(pipe))
	} else if *commands || *aliases {
		pipe = mapCommands(pipe)
	}

	if *aliases {
		pipe = mapAliases(pipe)
	}

	if *fsort >= 0 {
		pipe = sortJ(pipe, *fsort)
	}
	out := bufio.NewWriter(os.Stdout)
	if *bare {
		emitBare(pipe, out)
	} else {
		emitFormatted(pipe, out)
	}
	out.Flush()
}

// reads /proc/net/tcp or /proc/net/tcp6 to make a map of the listening sockets
// with
func getTCPMap(path string, ret map[string]string) (err error) {

	content, err := ioutil.ReadFile(path)
	if err != nil {
		return
	}

	lines := strings.Split(string(content), "\n")
	lines = lines[1 : len(lines)-1]
	words := make([]string, 20)
	for _, line := range lines {
		word := bytes.NewBuffer(make([]byte, 32))
		i := -1
		last := rune(0)
		for _, c := range line {
			if c == 32 {
				if i != -1 && last != 32 {
					words[i] = word.String()
					word.Reset()
					i++
				}
			} else {
				if i == -1 {
					i++
				}
				word.WriteByte(byte(c))
			}
			last = c
		}
		if len(words) > 8 {
			//OA is hex for socket state listening
			if words[3] == "0A" {
				if _, t := ret[words[9]]; !t {
					s := strings.Split(words[1], ":")
					if len(s) > 1 {
						p := s[len(s)-1]
						ret[words[9]] = p
					}
				}
			}
		}
	}
	return
}
