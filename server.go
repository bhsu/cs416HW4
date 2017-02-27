/*
 [worker-incoming ip:port] : the IP:port address that workers use to connect to the server
 [client-incoming ip:port] : the IP:port address that clients use to connect to the server
 go run worker.go [server ip:port]
 go run server.go [worker-incoming ip:port] [client-incoming ip:port]
*/

package main

import (
	"fmt"
	"log"
	"net"
	"net/rpc"
	"os"
	"sort"
	"strings"
	"time"
)

var (
	worker_incoming_ip_port string
	client_incoming_ip_port string
	workerIpArray           []string
	client                  *rpc.Client
	logger                  *log.Logger // Global logger.
	worker_port_UDP         = ":8080"
	worker_port_RPC         string
	arrayRTT                []int
)

// Resource server type (for RPC) with client
type MServer int

// Resource server type (for RPC) with worker
type Worker int

type MWebsiteReq struct {
	URI              string // URI of the website to measure
	SamplesPerWorker int    // Number of samples, >= 1
}

// Request that client sends in RPC call to MServer.GetWorkers
type MWorkersReq struct {
	SamplesPerWorker int // Number of samples, >= 1
}

// Response to:
// MServer.MeasureWebsite:
//   - latency stats per worker to a *URI*
//   - (optional) Diff map
// MServer.GetWorkers
//   - latency stats per worker to the *server*
type MRes struct {
	Stats map[string]LatencyStats    // map: workerIP -> LatencyStats
	Diff  map[string]map[string]bool // map: [workerIP x workerIP] -> True/False
}

type WorkerRes struct {
	WorkerIp string
	Min      int
	Median   int
	Max      int
	Md5Value string
}

// A stats struct that summarizes a set of latency measurements to an
// internet host.
type LatencyStats struct {
	Min    int // min measured latency in milliseconds to host
	Median int // median measured latency in milliseconds to host
	Max    int // max measured latency in milliseconds to host
}

func main() {

	done := make(chan int)
	args := os.Args[1:]

	// Missing command line args.
	if len(args) != 2 {
		fmt.Println("Usage: go run server.go [worker-incoming ip:port] [client-incoming ip:port]")
		return
	}

	worker_incoming_ip_port = args[0]                                 // setting command line args
	worker_port_RPC = splitIpToGetPortServer(worker_incoming_ip_port) // port will be used for rpc
	client_incoming_ip_port = args[1]
	fmt.Println("\nMain funtion: commandline args check worker_incoming_ip_port: ", worker_incoming_ip_port, "client_incoming_ip_port: ", client_incoming_ip_port, "\n")

	go InitServerWorkerRPC() // start listening for worker

	go InitServerClient() // start listening for client

	<-done
}

// start listening for client request
func InitServerClient() {
	fmt.Println("\nfunc InitServerClient: start listening for client")
	cServer := rpc.NewServer()
	c := new(MServer)
	cServer.Register(c)

	l, err := net.Listen("tcp", client_incoming_ip_port)
	ErrorCheck("func InitServerClient:", err, false)
	for {
		conn, err := l.Accept()
		ErrorCheck("func InitServerClient:", err, false)
		go cServer.ServeConn(conn)
	}
}

// start listening for worker request
func InitServerWorkerRPC() {
	fmt.Println("\nfunc InitServerWorkerRPC: start listening for worker")
	wServer := rpc.NewServer()
	w := new(Worker)
	wServer.Register(w)

	l, err := net.Listen("tcp", worker_incoming_ip_port)
	fmt.Println("worker_incoming_ip_port", worker_incoming_ip_port)
	ErrorCheck("fucn InitServerWorkerRPC:", err, false)
	for {
		conn, err := l.Accept()
		ErrorCheck("fucn InitServerWorkerRPC:", err, false)
		go wServer.ServeConn(conn)
	}
}

// receive worker ip via RPC
func (w *Worker) ReceiveWorkerIp(ip string, reply *bool) error {
	fmt.Println("\nfunc ReceiveWorkerIp: Received:", ip, "\n")
	workerIpArray = append(workerIpArray, ip) // add ip to the worker ip array
	*reply = true
	return nil
}

// rpc from client requesting web stats
func (c *MServer) MeasureWebsite(m MWebsiteReq, reply *MRes) error {

	resmap := make(map[string]LatencyStats)

	for i := 0; i < len(workerIpArray); i++ {

		re, err := sendGetWebRequestToWorker(workerIpArray[i], m)
		if err != nil {
			ErrorCheck("func MeasureWebsite:", err, false)
		} else {
			resmap[re.WorkerIp] = LatencyStats{re.Min, re.Median, re.Max}

		}
	}

	r := MRes{
		Stats: resmap,
	}
	*reply = r
	return nil
}

// rpc request to worker for getting web stats
func sendGetWebRequestToWorker(ip string, m MWebsiteReq) (WorkerRes, error) {
	var err error
	client = getRPCClientServer(ip + ":" + worker_port_RPC)

	req := MWebsiteReq{
		URI:              m.URI,
		SamplesPerWorker: m.SamplesPerWorker,
	}
	var res WorkerRes
	// Make RPC call.
	err = client.Call("Worker.GetWeb", req, &res)
	fmt.Println("\nfunc sendGetWebRequesttoWorker results:", "ip:", res.WorkerIp, "min:", res.Min, "median:", res.Median, "max:", res.Max, "mdhHashValue:", res.Md5Value)
	ErrorCheck("func sendGetWebRequesttoWorker:", err, false)
	return res, err
}

func (c *MServer) GetWorkers(m MWorkersReq, reply *MRes) error {
	resmap := make(map[string]LatencyStats)
	fmt.Println("\nfunc GetWorkers workerarray", workerIpArray, "\n")
	for i := 0; i < len(workerIpArray); i++ {
		re := getResRTT(workerIpArray[i]+worker_port_UDP, m.SamplesPerWorker)
		resmap[re.WorkerIp] = LatencyStats{re.Min, re.Median, re.Max}
	}
	r := MRes{
		Stats: resmap,
	}
	*reply = r
	return nil
}

func getResRTT(ipp string, times int) WorkerRes {
	array := ComputeRTT(ipp, times)
	min := computeMinRTT(array)
	median := computeMedianRTT(array)
	max := computeMaxRTT(array)
	ip := ipp

	r := WorkerRes{
		WorkerIp: ip,
		Min:      min,
		Median:   median,
		Max:      max,
	}

	return r
}

// ping -pong with workers
func ConnectToWorker(ip string) {
	fmt.Println("\nfunc ConnectToWorker: ping-pong request from server\n")
	timeoutDuration := 2 * time.Second // timeout set for 2 secs
	udpAddr, err := net.ResolveUDPAddr("udp", ip)

	ErrorCheck("func ConnectToWorker:", err, false)

	conn, err := net.DialUDP("udp", nil, udpAddr)
	ErrorCheck("func ConnectToWorker:", err, false)

	_, err = conn.Write([]byte("ping"))
	ErrorCheck("func ConnectToWorker:", err, false)

	var buf [512]byte

	err = conn.SetReadDeadline(time.Now().Add(timeoutDuration))
	n, err := conn.Read(buf[0:])
	fmt.Println("received:", string(buf[0:n]))
	ErrorCheck("func ConnectToWorker:", err, false)
	if e, ok := err.(net.Error); ok && e.Timeout() {
		// timeout
		_, err = conn.Write([]byte("ping"))
		ErrorCheck("func ConnectToWorker:", err, false)
		fmt.Println("received:", string(buf[0:n]))
	} else if err != nil {
		fmt.Println("err", err)
	}
	fmt.Println("\nfunc ConnectToWorker: done ping-pong request\n")
}

// computing rtt stats
func ComputeRTT(ip string, numberoftimes int) []int {

	for i := 0; i < numberoftimes; i++ {
		start := time.Now()
		// getting the uri response
		ConnectToWorker(ip)
		end := time.Now()
		diff := end.Sub(start)
		diffToSec := diff.Nanoseconds()
		diffToSec = diffToSec / 1000000
		diffToInt := int(diffToSec)

		arrayRTT = append(arrayRTT, diffToInt)
	}

	fmt.Println("\nfunc ComputeRTT RTT array:", arrayRTT, "\n")

	return arrayRTT
}

func computeMinRTT(array []int) int {
	sort.Ints(array)
	return array[0]
}

func computeMaxRTT(array []int) int {
	sort.Ints(array)
	i := len(array) - 1
	return array[i]
}

func computeMedianRTT(array []int) int {
	var median int
	sort.Ints(array)
	middle := ((len(array)) / 2)
	if len(array)%2 == 0 {
		medianA := array[middle]
		medianB := array[middle-1]
		median = (medianA + medianB) / 2
	} else {
		median = array[middle+1]
	}
	return median
}

func ErrorCheck(msg string, err error, exit bool) {
	if err != nil {
		log.Println(msg, err)
		if exit {
			os.Exit(-1)
		}
	}
}

// Create RPC client for contacting the server.
func getRPCClientServer(ip string) *rpc.Client {

	raddr, err := net.ResolveTCPAddr("tcp", ip)
	if err != nil {
		ErrorCheck("func getRPCClientServer:", err, false)
	}
	conn, err := net.DialTCP("tcp", nil, raddr)
	if err != nil {
		ErrorCheck("func getRPCClientServer:", err, false)
	}
	client := rpc.NewClient(conn)
	return client
}

func splitIpToGetPortServer(ip string) string {
	s := strings.Split(ip, ":")
	return s[1]
}
