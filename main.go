package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubectl/pkg/scheme"

	"github.com/milvus-io/milvus-sdk-go/v2/client"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

var MilvusMetricsName = map[string]bool{
	"milvus_proxy_sq_latency_bucket": true,
	"milvus_proxy_sq_latency_sum":    true,
	"milvus_proxy_sq_latency_count":  true,

	"milvus_proxy_mutation_latency_bucket": true,
	"milvus_proxy_mutation_latency_sum":    true,
	"milvus_proxy_mutation_latency_count":  true,

	"milvus_rootcoord_time_tick_delay": true,

	"milvus_proxy_search_vectors_count": true,
	"milvus_proxy_insert_vectors_count": true,

	"milvus_proxy_req_count": true,
}

type MilvusConfig struct {
	Namespace string `json:"namespace"`
	Release   string `json:"release"`
	Duration  int64  `json:"duration"`
	Interval  int64  `json:"interval"`
}

type Connection struct {
	Config       *MilvusConfig
	K8SClientset *kubernetes.Clientset
	RestCfg      *restclient.Config
	MilvusCli    client.Client
	writer       io.Writer
	done         chan bool
	v            chan *dto.MetricFamily
}

func main() {
	c := &Connection{
		Config: new(MilvusConfig),
		done:   make(chan bool),
		v:      make(chan *dto.MetricFamily),
	}

	// init
	c.InitConfig()
	c.InitK8SCli()
	c.InitMilvusCli()

	log.Println("start gather info.")
	// gather info
	go c.GatherBaseInfo()
	go c.GatherPprofInfo()

	f, err := os.Create("data/metrics-info")
	if err != nil {
		log.Fatal(err)
	}
	c.writer = f

	ticker := time.NewTicker(time.Duration(c.Config.Interval) * time.Second)
	go func() {
		for {
			select {
			case <-c.done:
				expfmt.FinalizeOpenMetrics(f)
				f.Close()
				return
			case <-ticker.C:
				c.GatherMetricsInfo()
			case v := <-c.v:
				expfmt.MetricFamilyToOpenMetrics(c.writer, v)
			}
		}
	}()

	time.Sleep(time.Duration(c.Config.Duration) * time.Second)
	ticker.Stop()
	c.done <- true

	log.Println("finish gather info.")
	select {}
}

func (c *Connection) InitMilvusCli() {
	service, err := c.K8SClientset.CoreV1().Services(c.Config.Namespace).Get(context.TODO(), c.Config.Release+"-milvus", metav1.GetOptions{})
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(time.Millisecond*80))
	defer cancel()
	cli, err := client.NewClient(ctx, client.Config{
		Address: service.Spec.ClusterIP + ":19530",
	})
	if err != nil {
		log.Fatal("failed to connect to milvus:", err.Error())
	}
	c.MilvusCli = cli

}

func (c *Connection) InitK8SCli() {
	/* out config
	homePath := homedir.HomeDir()
	if homePath == "" {
		log.Fatal("failed to get homedir")
	}

	kubeconfig := filepath.Join(homePath, ".kube", "config")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	*/
	config, err := restclient.InClusterConfig()
	if err != nil {
		log.Fatal(err)
	}
	c.RestCfg = config

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatal(err)
	}
	c.K8SClientset = clientset
}

func (c *Connection) InitConfig() {
	b, err := ioutil.ReadFile("config/config.json")
	if err != nil {
		log.Fatal("read config file err:", err)
	}
	err = json.Unmarshal(b, c.Config)
	if err != nil {
		log.Fatal("read config file err:", err)
	}
}

func (c *Connection) GatherBaseInfo() {
	file, err := os.Create("data/base-info")
	if err != nil {
		log.Fatal("create base-info err:", err.Error())
	}
	defer file.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(time.Millisecond*80))
	defer cancel()
	version, err := c.MilvusCli.GetVersion(ctx)
	if err != nil {
		log.Fatal("failed to get milvus version:", err.Error())
	}
	file.WriteString("the milvus version:" + version + "\n")

	deployments, err := c.K8SClientset.AppsV1().Deployments(c.Config.Namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Fatal(err)
	}
	file.WriteString("the deployments:")
	for _, deploy := range deployments.Items {
		if strings.HasPrefix(deploy.Name, c.Config.Release+"-milvus") {
			file.WriteString(deploy.Name + ",")
		}
	}
	file.WriteString("\n")
}

func (c *Connection) GatherMetricsInfo() {
	pods, err := c.K8SClientset.CoreV1().Pods(c.Config.Namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Println("get pod err:", err)
		return
	}
	go c.gatherMilvusMetricsInfo(pods)
	go c.gatherPodMetricsInfo(pods)
}

func (c *Connection) gatherMilvusMetricsInfo(pods *v1.PodList) {
	now := time.Now().UnixMilli()
	for _, pod := range pods.Items {
		if strings.HasPrefix(pod.Name, c.Config.Release+"-milvus") {
			uri := strings.Join([]string{"http://", pod.Status.PodIP, ":9091/metrics"}, "")
			resp, err := http.Get(uri)
			if err != nil {
				continue
			}
			defer resp.Body.Close()
			p := &expfmt.TextParser{}
			m, _ := p.TextToMetricFamilies(resp.Body)

			for key, v := range m {
				for index, _ := range m[key].Metric {
					m[key].Metric[index].TimestampMs = new(int64)
					*m[key].Metric[index].TimestampMs = now

				}
				if MilvusMetricsName[*v.Name] {
					c.v <- v
				}
			}
		}
	}
}

func (c *Connection) gatherPodMetricsInfo(pods *v1.PodList) {
	for _, pod := range pods.Items {
		if strings.HasPrefix(pod.Name, c.Config.Release+"-milvus") {
			buf := &bytes.Buffer{}
			errBuf := &bytes.Buffer{}
			request := c.K8SClientset.CoreV1().RESTClient().
				Post().
				Namespace(pod.Namespace).
				Resource("pods").
				Name(pod.Name).
				SubResource("exec").
				VersionedParams(&v1.PodExecOptions{
					Command: []string{"/bin/sh", "-c",
						"cat /sys/fs/cgroup/cpu/cpuacct.usage /sys/fs/cgroup/memory/memory.usage_in_bytes /sys/fs/cgroup/memory/memory.stat"},
					Stdin:  false,
					Stdout: true,
					Stderr: true,
					TTY:    true,
				}, scheme.ParameterCodec)
			exec, _ := remotecommand.NewSPDYExecutor(c.RestCfg, "POST", request.URL())
			exec.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
				Stdout: buf,
				Stderr: errBuf,
			})
			str := formatData(buf.String(), pod.Name)
			if str == "" {
				continue
			}
			p := &expfmt.TextParser{}
			m, _ := p.TextToMetricFamilies(strings.NewReader(str))
			for _, v := range m {
				c.v <- v
			}
		}
	}
}

func formatData(data string, label string) string {
	if data == "" {
		return ""
	}
	sc := bufio.NewScanner(strings.NewReader(data))
	sc.Scan()
	cpuUsage, _ := strconv.ParseUint(sc.Text(), 10, 64)
	cpuUsage /= 1000000000
	sc.Scan()
	memUsage, _ := strconv.ParseUint(sc.Text(), 10, 64)
	memStats := make(map[string]uint64)
	for sc.Scan() {
		t, v, err := ParseKeyValue(sc.Text())
		if err != nil {
			continue
		}
		memStats[t] = v
	}
	if v, ok := memStats["total_inactive_file"]; ok {
		if memUsage < v {
			memUsage = 0
		} else {
			memUsage -= v
		}
	}

	now := time.Now().UnixMilli()
	var b bytes.Buffer
	b.WriteString("# HELP container_cpu_usage_seconds_total Cumulative cpu time consumed in seconds.\n")
	b.WriteString("# TYPE container_cpu_usage_seconds_total counter\n")
	b.WriteString(fmt.Sprintf("container_cpu_usage_seconds_total{pod=\"%s\"} %d %d\n", label, cpuUsage, now))
	b.WriteString("# HELP container_memory_working_set_bytes Current working set in bytes.\n")
	b.WriteString("# TYPE container_memory_working_set_bytes gauge\n")
	b.WriteString(fmt.Sprintf("container_memory_working_set_bytes{pod=\"%s\"} %d %d\n", label, memUsage, now))

	return b.String()
}

func ParseKeyValue(t string) (string, uint64, error) {
	parts := strings.SplitN(t, " ", 3)
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("line %q is not in key value format", t)
	}

	value, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return "", 0, err
	}

	return parts[0], value, nil
}

func (c *Connection) GatherPprofInfo() {
	pods, err := c.K8SClientset.CoreV1().Pods(c.Config.Namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Println(err)
		return
	}
	for _, pod := range pods.Items {
		if strings.HasPrefix(pod.Name, c.Config.Release+"-milvus") {
			goroutineUri := strings.Join([]string{"http://", pod.Status.PodIP, ":9091/debug/pprof/goroutine?debug=2"}, "")
			profileUri := strings.Join([]string{"http://", pod.Status.PodIP, ":9091/debug/pprof/profile"}, "")
			go gatherPprofInfo(pod.Name, goroutineUri, "goroutine")
			go gatherPprofInfo(pod.Name, profileUri, "profile")
		}
	}
}

func gatherPprofInfo(name string, uri string, pattern string) {
	resp, _ := http.Get(uri)
	defer resp.Body.Close()
	file, _ := os.Create("data/" + pattern + "-" + name)
	defer file.Close()
	io.Copy(file, resp.Body)
}
