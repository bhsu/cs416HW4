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

	// setting command line args
	worker_incoming_ip_port = args[0]
	worker_port_RPC = splitIpToGetPortServer(worker_incoming_ip_port)
	client_incoming_ip_port = args[1]
	fmt.Println("\nMain funtion: commandline args check worker_incoming_ip_port: ", worker_incoming_ip_port, "client_incoming_ip_port: ", client_incoming_ip_port, "\n")

	go InitServerWorkerRPC() // InitServerWorker()

	go InitServerClient() // listening for client request

	<-done
}

func InitServerClient() {
	fmt.Println("\nInitServerClient called\n")
	cServer := rpc.NewServer()
	c := new(MServer)
	cServer.Register(c)

	l, err := net.Listen("tcp", client_incoming_ip_port)
	ErrorCheck("InitServerClient", err, false)
	for {
		conn, err := l.Accept()
		ErrorCheck("InitServerClient", err, false)
		go cServer.ServeConn(conn)
	}
}

func InitServerWorkerRPC() {
	fmt.Println("InitServerWorkerRPC called")
	wServer := rpc.NewServer()
	w := new(Worker)
	wServer.Register(w)

	l, err := net.Listen("tcp", worker_incoming_ip_port)
	fmt.Println("worker_incoming_ip_port", worker_incoming_ip_port)
	ErrorCheck("InitServerClient", err, false)
	for {
		conn, err := l.Accept()
		ErrorCheck("InitServerClient", err, false)
		go wServer.ServeConn(conn)
	}
}

// receive worker ip via RPC
func (w *Worker) ReceiveWorkerIp(ip string, reply *bool) error {
	fmt.Println("\nReceiveWorkerIpcalled\n")
	// add ip
	fmt.Println("\nip", ip, "\n")
	workerIpArray = append(workerIpArray, ip)
	fmt.Println("\nworkerIpArray:", workerIpArray, "\n")
	*reply = true
	return nil
}

func (c *MServer) MeasureWebsite(m MWebsiteReq, reply *MRes) error {

	fmt.Println("\nMeasureWebsite called\n")

	resmap := make(map[string]LatencyStats)

	for i := 0; i < len(workerIpArray); i++ {

		re, err := sendGetWebRequesttoWorker(workerIpArray[i], m)
		if err != nil {
			fmt.Println("err", err)
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

func sendGetWebRequesttoWorker(ip string, m MWebsiteReq) (WorkerRes, error) {
	fmt.Println("\nsendGetWebRequesttoWorker called\n")
	var err error
	client = getRPCClientServer(ip + ":" + worker_port_RPC)
	fmt.Println("\nsendGetWebRequesttoWorker client", client, "\n")

	req := MWebsiteReq{
		URI:              m.URI,
		SamplesPerWorker: m.SamplesPerWorker,
	}
	var res WorkerRes
	// Make RPC call.
	err = client.Call("Worker.GetWeb", req, &res)
	fmt.Println("\nsendGetWebRequesttoWorker res:", res, "\n")
	ErrorCheck("", err, false)
	return res, err
}

func (c *MServer) GetWorkers(m MWorkersReq, reply *MRes) error {
	fmt.Println("\nGetWorkers called\n")
	fmt.Println("\nGetWorkers m", m.SamplesPerWorker)
	resmap := make(map[string]LatencyStats)
	//workerIpArray = append(workerIpArray, "127.0.0.1")
	for i := 0; i < len(workerIpArray); i++ {
		fmt.Println("GetWorkers workerarray", workerIpArray)
		re := getResRTT(workerIpArray[i]+worker_port_UDP, m.SamplesPerWorker)

		resmap[re.WorkerIp] = LatencyStats{re.Min, re.Median, re.Max}
		fmt.Println("GetWorkers resmap", resmap)
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
	fmt.Println("getweb ip,min,median,max:", ip, min, median, max)

	return r
}

// FIXME hardcoded
func ConnectToWorker(ip string) {
	fmt.Println("ConnectToWorker called ip", ip)
	// timeout set for 2 secs
	timeoutDuration := 2 * time.Second
	udpAddr, err := net.ResolveUDPAddr("udp", ip)
	fmt.Println("ConnectToWorker ip", ip)
	ErrorCheck("udp1", err, false)

	conn, err := net.DialUDP("udp", nil, udpAddr)
	ErrorCheck("udp2", err, false)

	_, err = conn.Write([]byte("ping"))
	ErrorCheck("udp3", err, false)

	var buf [512]byte

	err = conn.SetReadDeadline(time.Now().Add(timeoutDuration))
	n, err := conn.Read(buf[0:])
	ErrorCheck("udp4", err, false)
	if e, ok := err.(net.Error); ok && e.Timeout() {
		// timeout
		_, err = conn.Write([]byte("ping"))
		ErrorCheck("udp3", err, false)
		fmt.Println("received:", string(buf[0:n]))
	} else if err != nil {
		fmt.Println("err", err)
	}

}

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
		log.Printf("\nRTT took %s", diffToInt)

		arrayRTT = append(arrayRTT, diffToInt)
	}

	fmt.Println("\arrayRTT", arrayRTT, "\n")

	return arrayRTT
}

func computeMinRTT(array []int) int {
	sort.Ints(array)
	fmt.Println("\nmin number in array: ", array[0], "\n")
	return array[0]
}

func computeMaxRTT(array []int) int {
	sort.Ints(array)
	i := len(array) - 1
	fmt.Println("max number in array: ", array[i], "\n")
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
	fmt.Println("\nmedian number in array: ", median, "\n")
	return median
}

func ErrorCheck(msg string, err error, exit bool) {
	//fmt.Println("\nErrorCheck called\n")
	if err != nil {
		log.Println(msg, err)
		if exit {
			os.Exit(-1)
		}
	}
}

// Create RPC client for contacting the server.
func getRPCClientServer(ip string) *rpc.Client {
	fmt.Println("\ngetRPCClientServer called\n")
	raddr, err := net.ResolveTCPAddr("tcp", ip)
	fmt.Println("\ngetRPCClientServer raddr", raddr)
	if err != nil {
		fmt.Println("err1")
		logger.Fatal(err)
	}
	conn, err := net.DialTCP("tcp", nil, raddr)
	fmt.Println("\ngetRPCClientServer conn", conn)
	if err != nil {
		fmt.Println("err2")
		logger.Fatal(err)
	}
	client := rpc.NewClient(conn)
	fmt.Println("\ngetRPCClientServer client", client)
	return client
}

func splitIpToGetPortServer(ip string) string {
	fmt.Println("splitIpToGetPortServer called")
	s := strings.Split(ip, ":")
	fmt.Println("splitIpToGetPortServer", worker_port_RPC)
	return s[1]
}
