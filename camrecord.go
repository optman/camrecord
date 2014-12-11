package main

import "fmt"
import "net"
import "bufio"
import "strings"
import "strconv"
import "encoding/base64"
import "os"
import "bytes"
import "time"
import "flag"

var fuaBuffer *bytes.Buffer
var f *os.File

var  server = flag.String("server", "0.0.0.0:554", "server address host:port")
var  recordMinutes = flag.Int("t", 1, "record time length in minutes")
var  outputFile = flag.String("o", "", "output file")


func readHeader(reader *bufio.Reader) map[string]string {
	result := make(map[string]string)

	for{
		line, _, err := reader.ReadLine();
		if err != nil{
			panic(err)
		}
		if string(line)=="" {
			break
		}

		fmt.Println(string(line))

		keyvalue := strings.SplitN(string(line),":", 2)	
		if len(keyvalue) > 1{
			result[strings.ToUpper(keyvalue[0])] = strings.TrimSpace(keyvalue[1])
		}
	}

	fmt.Println()

	return result

}

func readBody(reader *bufio.Reader, contentLength int) (result string){
	if(contentLength < 0){

		buf := make([]byte, 512)
		for{
			read, _ := reader.Read(buf)
			if read == 0 {
				return
			}else{
				result += string(buf)
			}

		}
	}else{
		buf := make([]byte, contentLength)
		start := 0

		for ;start < contentLength; {
			readed, _ := reader.Read(buf[start:])
			result += string(buf)

			//fmt.Print(string(buf[start:start+readed]))

			start += readed
		}

		//fmt.Printf("read %v bytes\n", contentLength)

	}

	return result
}

func request(conn net.Conn, reader *bufio.Reader, action, url string, hs map[string]string, seq int) (map[string]string, string){

	req := action + " " + url + " RTSP/1.0\r\n"
	for k, v := range hs{
		req += k + ": " + v + "\r\n"
	}
	req += fmt.Sprintf("CSeq: %d\r\n", seq)
	//req += "User-Agent: test\r\n"
	//req += "Content-Length: 0\r\n"
	req += "\r\n"

	fmt.Print(req)

	conn.Write([]byte(req))

	//reader := bufio.NewReader(conn)

	headers := readHeader(reader)
	//fmt.Println(headers)

	contentLength := 0
	value, ok := headers["CONTENT-LENGTH"]
	if ok {
		contentLength, _ = strconv.Atoi(value)
	}

	//fmt.Printf("contentLength = %v\r\n", contentLength)

	if contentLength != 0{
		return headers, readBody(reader, contentLength)
	}

	return headers, ""
}

func main(){

	flag.Parse()


	conn, err := net.Dial("tcp", *server);

	if err != nil {
		fmt.Printf("connect server fail...%v\n", err);
		return
	}

	url := "rtsp://" + *server + "/"
	seq := 1

	reader := bufio.NewReader(conn)

	headers, body := request(conn, reader, "DESCRIBE", url, nil, seq)
	seq++

	fmt.Println(body)

	urlBase := ""
	urlBase , _ = headers["CONTENT-BASE"]
	//fmt.Println(urlBase)'

	var ppsSps [][]byte
	startPos := strings.Index(body, "sprop-parameter-sets")
	if startPos > 0{

		startPos += strings.Index(body[startPos:], "=")
		endPos := startPos + strings.Index(body[startPos:], "\r\n")
		ppsSpsBase64 := strings.Split(body[startPos + 1:endPos], ",")
		//fmt.Println(ppsSpsBase64)
		ppsSps = make([][]byte, len(ppsSpsBase64))
		for i, item := range ppsSpsBase64{
			ppsSps[i], _ = base64.StdEncoding.DecodeString(item)
		}
	}

	var controlUrl string
	startPos = strings.Index(body, "video")
	startPos += strings.Index(body[startPos:], "control")
	if startPos > 0{
		startPos += strings.Index(body[startPos:], ":")
		endPos := startPos + strings.Index(body[startPos:], "\r\n")
		controlUrl = body[startPos+1:endPos]
		//fmt.Println(controlUrl)
	}

	playUrl := urlBase + controlUrl 

	headers, _ = request(conn, reader, "SETUP", playUrl, (map[string]string{
		"Transport" : "RTP/AVP/TCP;unicast;interleaved=0-1",
		}), seq)
	seq++


	session, _ := headers["SESSION"]
	headers, _ = request(conn, reader, "PLAY", playUrl, (map[string]string{
		"Session" : session,
		}), seq)
	seq++


	f, err = os.Create(*outputFile)
	defer func(){
		f.Close()
	}()

	if err != nil{
		fmt.Println(err)
		return
	}

	for _, pps := range ppsSps{
		onNalu(pps)
	}

	go func(){
		for{
			conn, _ := net.Dial("tcp", *server);
			reader := bufio.NewReader(conn)
			_, body := request(conn, reader, "GET_PARAMETER", playUrl, (map[string]string{
				"Session" : session,
				}), seq)
			seq++

			fmt.Println(body)

			conn.Close()
			conn = nil

			time.Sleep(30*time.Second)
		}
	}()

	go func(){
		readAVPData(reader)
	}()

	<- time.After(time.Duration(*recordMinutes)*time.Minute)

}

func readAVPData(reader *bufio.Reader){

	//totalBytes := 0

	for {

	magicNum, err := reader.ReadByte()
	channel, _ := reader.ReadByte()
	dataLengthHi, _ := reader.ReadByte()
	dataLengthLow, _ := reader.ReadByte()
	dataLength := (int)(dataLengthHi)*int(256) + (int)(dataLengthLow)
	data := make([]byte, dataLength)
	offset := 0
	readed := 0
	for ;offset < dataLength; offset += readed{
		readed, err = reader.Read(data[offset:])
		if err != nil{
			break
		}
	}

	if err != nil{
		fmt.Println(err)
		break
	}

	fmt.Printf("magicNum %d channel %d dataLength %d\n", magicNum, channel, dataLength)

	if magicNum != 36{
		fmt.Println("magicNum wrong!")
		break
	}

	if channel != 0{
		continue
	}



	/*
	totalBytes += dataLength

	if totalBytes > 100*1024{
		break
	}*/


	decodeRtp(data)


	}//end for

}

func decodeRtp(data []byte){

	payload :=data[12:]

	naluType := payload[0] & 0x1f;
	if(naluType > 0 && naluType < 23){
		onNalu(payload)
	}else if(naluType == 28){
		onFuA(payload)
	}else{
		fmt.Println("unknown type");
	}
}

func onNalu(data []byte){
	f.Write([]byte{0,0,0,1})
	f.Write(data)
}

func onFuA(data []byte){
	naluType := data[0] & 0xe0
	naluType  |= data[1] &  0x1f

	start := data[1] >> 7
	end := (data[1] >> 6) & 0x01

	if start > 0{
		fuaBuffer = bytes.NewBuffer(nil)
		fuaBuffer.WriteByte(naluType)
	}

	fuaBuffer.Write(data[2:])

	if end > 0{
		onNalu(fuaBuffer.Bytes())
	}
}
