package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/shirou/gopsutil/process"
	"github.com/vmihailenco/msgpack/v5"
	"go.nanomsg.org/mangos/v3"
	"go.nanomsg.org/mangos/v3/protocol/sub"
	_ "go.nanomsg.org/mangos/v3/transport/all"
	"golang.org/x/sync/errgroup"
)

var (
	g errgroup.Group
	h = flag.Bool("h", false, "help usage")
	x = flag.Bool("X", false, "debug")
	t = flag.String("t", "bs2radiuis", "api token")
)

var (
	// 匹配IP4
	ip4Str = `((25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(25[0-5]|2[0-4]\d|[01]?\d\d?)`

	// 匹配IP6，参考以下网页内容：
	// http://blog.csdn.net/jiangfeng08/article/details/7642018
	ip6Str = `(([0-9A-Fa-f]{1,4}:){7}([0-9A-Fa-f]{1,4}|:))|` +
		`(([0-9A-Fa-f]{1,4}:){6}(:[0-9A-Fa-f]{1,4}|((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3})|:))|` +
		`(([0-9A-Fa-f]{1,4}:){5}(((:[0-9A-Fa-f]{1,4}){1,2})|:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3})|:))|` +
		`(([0-9A-Fa-f]{1,4}:){4}(((:[0-9A-Fa-f]{1,4}){1,3})|((:[0-9A-Fa-f]{1,4})?:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:))|` +
		`(([0-9A-Fa-f]{1,4}:){3}(((:[0-9A-Fa-f]{1,4}){1,4})|((:[0-9A-Fa-f]{1,4}){0,2}:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:))|` +
		`(([0-9A-Fa-f]{1,4}:){2}(((:[0-9A-Fa-f]{1,4}){1,5})|((:[0-9A-Fa-f]{1,4}){0,3}:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:))|` +
		`(([0-9A-Fa-f]{1,4}:){1}(((:[0-9A-Fa-f]{1,4}){1,6})|((:[0-9A-Fa-f]{1,4}){0,4}:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:))|` +
		`(:(((:[0-9A-Fa-f]{1,4}){1,7})|((:[0-9A-Fa-f]{1,4}){0,5}:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:))`

	// 同时匹配IP4和IP6
	ipStr = "(" + ip4Str + ")|(" + ip6Str + ")"

	ipre = regexp.MustCompile("^" + ipStr + "$")
)

const RadClientsTpl = `{{define "radclients"}}
client localhost {
	ipaddr = 127.0.0.1
	proto = *
	secret = testing123
	require_message_authenticator = no
	nas_type	 = other
	limit {
		max_connections = 16
		lifetime = 0
		idle_timeout = 30
	}
}

client any {
        ipaddr          = 0.0.0.0/0
        secret          = mysecret
}

{{range .vpes}}    
client client_{{.VpeName}} {
        ipaddr          = {{.Ipaddr}}
        secret          = {{.Secret}}
}

{{end}}

{{end}}

`

func inSlice(v string, sl []string) bool {
	for _, vv := range sl {
		if vv == v {
			return true
		}
	}
	return false
}

func startFreeradius() {
	defer func() {
		if err := recover(); err != nil {
			log.Println(err)
		}
	}()
	log.Println("start freeradius ")
	cmd := exec.Command("/usr/sbin/freeradius")
	if *x {
		cmd = exec.Command("/usr/sbin/freeradius", "-X")
	}
	cmd.Stdin = os.Stderr
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	// cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err := cmd.Start()
	if err != nil {
		log.Println(err)
	}
	err = cmd.Wait()
	if err != nil {
		log.Println(err)
	}
}

type FreeradiusClient struct {
	VpeName string
	Ipaddr  string
	Secret  string
}

type Service struct {
	Sock mangos.Socket
}

func NewService() *Service {
	return &Service{}
}

func (t *Service) Release() error {
	return t.Sock.Close()
}

const FreeradiusClientsTopic = "FreeradiusClients"

func (t *Service) Startsubscriber(targetAddr string) error {
	var sock mangos.Socket
	var err error
	var topicBytes = []byte(FreeradiusClientsTopic)

	if sock, err = sub.NewSocket(); err != nil {
		return fmt.Errorf("subscriber Create error %s", err.Error())
	}
	if err = sock.SetOption(mangos.OptionSubscribe, topicBytes); err != nil {
		return fmt.Errorf("subscriber sub error %s", err.Error())
	}
	_ = sock.SetOption(mangos.OptionDialAsynch, true)

	if err = sock.Dial(targetAddr); err != nil {
		return fmt.Errorf("subscriber connect to %s error %s", targetAddr, err.Error())
	}

	log.Println(fmt.Sprintf("subscriber Connected to  %s, topic=%s", targetAddr, FreeradiusClientsTopic))

	for {
		msg, err := sock.Recv()
		if err != nil {
			if err != nil {
				log.Printf("subscriber recv Message error %s", err.Error())
				continue
			}
		}
		var smsg = make([]FreeradiusClient, 0)
		err = msgpack.Unmarshal(msg[len(topicBytes):], &smsg)
		if err != nil {
			log.Printf("subscriber Unmarshal Message(%s) error %s", string(msg), err.Error())
			continue
		}
		data2 := make([]FreeradiusClient, 0)
		ks := make([]string, 0)
		for _, vpe := range smsg {
			if vpe.Ipaddr == "127.0.0.1" || vpe.Secret == "" || inSlice(vpe.Ipaddr, ks) || !ipre.MatchString(vpe.Ipaddr) {
				continue
			} else {
				data2 = append(data2, vpe)
			}
		}

		buff := new(strings.Builder)
		tp, err := template.New("TP").Parse(RadClientsTpl)
		err = tp.ExecuteTemplate(buff,"radclients", map[string]interface{}{
			"vpes": data2,
		})
		if err != nil {
			continue
		}
		cfgfile := os.Getenv("FREERADIUS_CLIENT_CONFIG_FILE")
		if cfgfile == "" {
			cfgfile = "/etc/freeradius/clients.conf"
		}
		cdata := buff.String()
		log.Println(cdata)
		err = ioutil.WriteFile(strings.TrimSpace(cfgfile), []byte(cdata), 0644)
		if err != nil {
			log.Println(err)
			continue
		}
		KillRadiusProc()
	}
}

func startCheckProc() {
	ticker := time.NewTicker(time.Millisecond * 5000)
	go func() {
		for t := range ticker.C {
			_ = t.String()
			ps, _ := process.Processes()
			count := 0
			for _, p := range ps {
				name, _ := p.Name()
				st, _ := p.Status()
				if strings.Contains(name, "freeradius") {
					// log.Println()(fmt.Sprintf("%s %s", name, st))
					if st == "Z" {
						log.Println(fmt.Sprintf("%s %s", name, st))
						// syscall.Kill(int(p.Pid), syscall.SIGKILL)
						p.Resume()
					}
					time.Sleep(time.Second * 3)
					if st, _ := p.Status(); st == "S" {
						count += 1
					}
				}
			}
			if count == 0 {
				go startFreeradius()
			}
		}
	}()
}

func KillRadiusProc() {
	defer func() {
		if err := recover(); err != nil {
			log.Println(err)
		}
	}()
	ps, _ := process.Processes()
	for _, p := range ps {
		name, _ := p.Name()
		user ,_ := p.Username()
		if strings.Contains(name, "freeradius") && user == "freerad" {
			syscall.Kill(int(p.Pid), syscall.SIGKILL)
		}
	}
}

func main() {
	flag.Parse()

	if *h == true {
		ustr := "daemon version: daemon/1.0, Usage:\ndaemon -h\nOptions:"
		fmt.Fprintf(os.Stderr, ustr)
		flag.PrintDefaults()
		return
	}

	startCheckProc()

	g.Go(func() error {
		s := NewService()
		err := s.Startsubscriber(os.Getenv("TEAMSACS_MESSAGE_PUB_ADDRESS"))
		defer s.Release()
		return err
	})

	if err := g.Wait(); err != nil {
		log.Fatal(err)
	}

}
