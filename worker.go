package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"sort"
	"strings"
	"time"
)

var (
	server_ip_port    string
	measurementsArray []int
	workerport_UDP    = ":8080"
	workerport_RPC    string
	localaddress      string
	client            *rpc.Client // RPC client.
	md5HashValue      string
)

// Resource server type.
type Worker int

type MWebsiteReq struct {
	URI              string // URI of the website to measure
	SamplesPerWorker int    // Number of samples, >= 1
}

// Response to:
// MServer.MeasureWebsite:
//   - latency stats per worker to a *URI*
//   - (optional) Diff map
// MServer.GetWorkers
//   - latency stats per worker to the *server*
type MRes struct {
	Stats map[string]LatencyStats // map: workerIP -> LatencyStats
	//Diff  map[string]map[string]bool // map: [workerIP x workerIP] -> True/False
}

// A stats struct that summarizes a set of latency measurements to an
// internet host.
type LatencyStats struct {
	Min    int // min measured latency in milliseconds to host
	Median int // median measured latency in milliseconds to host
	Max    int // max measured latency in milliseconds to host
}

type WorkerRes struct {
	WorkerIp string
	Min      int
	Median   int
	Max      int
	Md5Value string
}

func main() {

	args := os.Args[1:]
	done := make(chan int)

	// Missing command line args.
	if len(args) != 1 {
		fmt.Println("Usage: go run server.go [worker-incoming ip:port] [client-incoming ip:port]")
		return
	}

	server_ip_port = args[0]
	workerport_RPC = splitIpToGetPort(server_ip_port)
	fmt.Println("\nMain funtion: commandline args check server_ip_port: ", server_ip_port, "\n")

	//GetOutboundIP()               // get local ip
	getExternalIp()
	client = getRPCClientWorker() // Create RPC client for contacting the server.
	sendWorkerIptoServer(client)  // send local ip to server to store

	go InitServerWorkerUDP() // start listening for udp from server

	<-done
}

// fetch the requested page
func fetchPage(uri string, numberoftimes int) []int {

	fmt.Println("\nfunc fetchPage uri", uri, "numberoftimes", numberoftimes)

	var diffToInt int
	// timing the request to uri
	for i := 0; i < numberoftimes; i++ {
		start := time.Now()
		// getting the uri response
		response, err := http.Get(uri)
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			end := time.Now()
			diff := end.Sub(start)
			diffToSec := diff.Seconds()
			diffToSec = diffToSec * 1000
			diffToInt = int(diffToSec)
			fmt.Println("\nfunc fetchPage took", diffToInt)
		}
		if err != nil {
			fmt.Println(err)
		} else {
			response.Body.Close()
			//_, err := io.Copy(os.Stdout, response.Body)
			if err != nil {
				fmt.Println(err)
			}
		}
		md5HashValue = getMD5Hash(response.Body)
		measurementsArray = append(measurementsArray, diffToInt)
	}

	fmt.Println("\nfunc fetchPage nmeasurementsArray:", measurementsArray, "\n")
	return measurementsArray
}

func checkError(msg string, err error, exit bool) {
	if err != nil {
		log.Println(msg, err)
		if exit {
			os.Exit(-1)
		}
	}
}

func computeMin(array []int) int {
	sort.Ints(array)
	return array[0]
}

func computeMax(array []int) int {
	sort.Ints(array)
	i := len(array) - 1
	return array[i]
}

func computeMedian(array []int) int {
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

// Get preferred outbound ip of this machine
func GetOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().String()
	idx := strings.LastIndex(localAddr, ":")
	localaddress = localAddr[0:idx]
	fmt.Println("\nfunc getlocaladdress:", localaddress, "\n")
	return localaddress
}

// Create RPC client for contacting the server.
func getRPCClientWorker() *rpc.Client {

	raddr, err := net.ResolveTCPAddr("tcp", server_ip_port)
	fmt.Println("\nfunc getRPCClientWorker: workerport_RPC", workerport_RPC)
	fmt.Println("\nfunc getRPCClientWorker server_ip_port", server_ip_port)
	if err != nil {
		checkError("func getRPCClientWorker:", err, false)
	}
	conn, err := net.DialTCP("tcp", nil, raddr)
	if err != nil {
		checkError("func getRPCClientWorker:", err, false)
	}
	client := rpc.NewClient(conn)
	return client
}

// sends local ip to server
func sendWorkerIptoServer(client *rpc.Client) (bool, error) {
	var reply bool
	var err error
	err = client.Call("Worker.ReceiveWorkerIp", localaddress, &reply) // send localaddress to the server to store
	checkError("\nfunc sendWorkerIptoServer:", err, false)
	client.Close()           // close the client, so we can start listening
	go InitWorkerServerRPC() // start listening
	return reply, err
}

func InitWorkerServerRPC() {
	fmt.Println("\nfunc InitWorkerServerRPC: start listening for server rpc")
	wServer := rpc.NewServer()
	w := new(Worker)
	wServer.Register(w)

	ip := localaddress + ":" + workerport_RPC
	fmt.Println("InitWorkerServerRPC ip", ip)

	l, err := net.Listen("tcp", ip)
	checkError("InitWorkerServerRPC", err, false)
	for {
		fmt.Println("\nfunc InitWorkerServerRPC: start listening for server rpc")
		conn, err := l.Accept()
		checkError("\n func InitWorkerServerRPC:", err, false)
		go wServer.ServeConn(conn)
	}
}

func (w *Worker) GetWeb(m MWebsiteReq, reply *WorkerRes) error {

	array := fetchPage(m.URI, m.SamplesPerWorker)
	min := computeMin(array)
	median := computeMedian(array)
	max := computeMax(array)
	ip := localaddress

	r := WorkerRes{
		WorkerIp: ip,
		Min:      min,
		Median:   median,
		Max:      max,
		Md5Value: md5HashValue,
	}
	fmt.Println("\nfunc GetWeb: ip:", ip, "min:", min, "median:", median, "max:", max, "md5:", md5HashValue)
	*reply = r
	return nil
}

func splitIpToGetPort(ip string) string {
	s := strings.Split(ip, ":")
	return s[1]
}

// initialize worker to listen for server
func InitServerWorkerUDP() {
	fmt.Println("\nfunc InitServerWorkerUDP: start listening for server udp: localaddress is", localaddress+workerport_UDP)
	udpAddr, err := net.ResolveUDPAddr("udp", localaddress+workerport_UDP)
	checkError("\nfunc InitServerWorkerUDP:", err, false)
	conn, err := net.ListenUDP("udp", udpAddr)
	checkError("\nfunc InitServerWorkerUDP:", err, false)
	for {
		handleUDPConn(conn)
	}
}

func handleUDPConn(conn *net.UDPConn) {
	var buf [512]byte
	n, addr, err := conn.ReadFromUDP(buf[0:])
	if err != nil {
		return
	}
	fmt.Println("\nfunc handleUDPConn: recevied", string(buf[0:n]))

	if string(buf[0:n]) == "ping" {
		msg := "pong"
		conn.WriteToUDP([]byte(msg), addr)
		fmt.Println("\nfunc handleUDPConn: write pong back to server")
	}

}

func getMD5Hash(file io.ReadCloser) string {
	buf := new(bytes.Buffer)
	buf.ReadFrom(file)
	newStr := buf.String()

	hash := md5.Sum([]byte(newStr))
	return hex.EncodeToString(hash[:])

}

func getExternalIp(){
	fmt.Println("getexternalIP called")

	resp, err := http.Get("http://myexternalip.com/raw")
	if err != nil {
		os.Stderr.WriteString(err.Error())
		os.Stderr.WriteString("\n")
		os.Exit(1)
	}
	defer resp.Body.Close()
	//io.Copy(os.Stdout, resp.Body)

	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	newStr := buf.String()
	localaddress = strings.TrimSpace(newStr)
	fmt.Println("getexternalIP", localaddress)

}
