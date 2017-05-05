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
)

type holder struct {
	pid   string
	inode string
	port  int64
	ports string
	cmd   []byte
	lock  sync.Mutex
}

var once sync.Once
var proc = "/proc"

//findPids returns a channel of FileInfo matching the pattern /proc/[pid]/
// these are the directories containing process information in linux
func findPids() chan os.FileInfo {
	out := make(chan os.FileInfo)
	go func() {
		defer close(out)
		if d, err := os.OpenFile(proc, os.O_RDONLY, 000); err != nil {
			panic(err)  //bail if we can't read the /proc dir
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
// and passes one holder per pid to out channel, with ports set
func gather(in <-chan holder) chan holder {
	out := make(chan holder)
	m := make(map[string]holder)

	go func() {
		for n := range in {
			if v, t := m[n.pid]; t {
				s := "," + strconv.FormatInt(n.port, 10)
				v.ports = v.ports + s
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

func socketFromLink(p string) (string, bool) {
	if s, e := os.Readlink(p); e == nil {
		if strings.HasPrefix(s, "socket:[") {
			s = s[8 : len(s) - 1]
			return s, true
		}
	}
	return "", false

}

//emitBare terminates the pipeline and writes a tab separated line terminated
// with \\n to buf per holder received on the in channel
func emitBare(in <-chan holder, buf io.Writer) {
	for p := range in {
		if p.ports != "" {
			fmt.Fprintf(buf, "%s\t%s\t\t%s\n", p.pid, p.ports, p.cmd)
		} else {
			fmt.Fprintf(buf, "%s\t%d\t\t%s\n", p.pid, p.port, p.cmd)
		}
	}
}

//emitFormatted terminates the pipeline and writes a colorized and formatted
// table to buf
func emitFormatted(in <-chan holder, buf io.Writer){

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

	fmt.Fprint(buf,result)


}

func main() {

	commands := flag.BoolP("command", "c", false, "show commandlines")
	bare := flag.BoolP("bare", "b", false, "clean output, ")
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
	} else if *commands {
		pipe = mapCommands(pipe)
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
	lines = lines[1 : len(lines) - 1]
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
						p := s[len(s) - 1]
						ret[words[9]] = p
					}
				}
			}
		}
	}
	return
}
