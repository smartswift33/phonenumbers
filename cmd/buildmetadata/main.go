package main

import (
	"bufio"
	"compress/gzip"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"bytes"

	"github.com/nyaruka/phonenumbers"
	"google.golang.org/protobuf/proto"
)

type prefixBuild struct {
	url     string
	dir     string
	srcPath string
	varName string
}

const (
	metadataURL  = "https://raw.githubusercontent.com/googlei18n/libphonenumber/master/resources/PhoneNumberMetadata.xml"
	metadataPath = "metadata_bin.go"

	shortNumberMetadataURL  = "https://raw.githubusercontent.com/googlei18n/libphonenumber/master/resources/ShortNumberMetadata.xml"
	shortNumberMetadataPath = "shortnumber_metadata_bin.go"

	tzURL  = "https://raw.githubusercontent.com/googlei18n/libphonenumber/master/resources/timezones/map_data.txt"
	tzPath = "prefix_to_timezone_bin.go"
	tzVar  = "timezoneMapData"

	regionPath = "countrycode_to_region_bin.go"
	regionVar  = "regionMapData"
)

var carrier = prefixBuild{
	url:     "https://github.com/googlei18n/libphonenumber/trunk/resources/carrier",
	dir:     "carrier",
	srcPath: "prefix_to_carriers_bin.go",
	varName: "carrierMapData",
}

var geocoding = prefixBuild{
	url:     "https://github.com/googlei18n/libphonenumber/trunk/resources/geocoding",
	dir:     "geocoding",
	srcPath: "prefix_to_geocodings_bin.go",
	varName: "geocodingMapData",
}

func fetchURL(url string) []byte {
	resp, err := http.Get(url)
	if err != nil || resp.StatusCode != 200 {
		log.Fatalf("Error fetching URL '%s': %s", url, err)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Error reading body: %s", err)
	}

	return body
}

func svnExport(dir string, url string) {
	os.RemoveAll(dir)
	cmd := exec.Command(
		"/bin/bash",
		"-c",
		fmt.Sprintf("svn export %s --force", url),
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalf("error calling svn export: %s", err.Error())
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Fatalf("error calling svn export: %s", err.Error())
	}
	if err = cmd.Start(); err != nil {
		log.Fatalf("error calling svn export: %s", err.Error())
	}
	data, err := ioutil.ReadAll(stderr)
	if err != nil {
		log.Fatalf("error reading svn export: %s : %s", err.Error(), data)
	}
	outputBuf := bufio.NewReader(stdout)

	for {
		output, _, err := outputBuf.ReadLine()
		if err != nil {
			if err != io.EOF {
				log.Fatal(err)
			}
			break
		}
		log.Println(string(output))
	}

	if err = cmd.Wait(); err != nil {
		log.Fatal(err)
	}
}

func writeFile(filePath string, data []byte) {
	// file should already exist (likely running from wrong directory)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		log.Fatalf("no such file: %s make sure you are running from the root of the repo directory", filePath)
	}

	fmt.Printf("Writing new %s\n", filePath)
	err := ioutil.WriteFile(filePath, data, os.FileMode(0664))
	if err != nil {
		log.Fatalf("Error writing '%s': %s", filePath, err)
	}
}

func buildRegions(metadata *phonenumbers.PhoneMetadataCollection) {
	log.Println("Building region map")
	regionMap := phonenumbers.BuildCountryCodeToRegionMap(metadata)
	writeIntStringArrayMap(regionPath, regionVar, regionMap)
}

func buildTimezones() {
	log.Println("Building timezone map")
	body := fetchURL(tzURL)

	// build our map of prefix to timezones
	prefixMap := make(map[int32][]string)
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}

		if strings.TrimSpace(line) == "" {
			continue
		}

		fields := strings.Split(line, "|")
		if len(fields) != 2 {
			log.Fatalf("Invalid format in timezone file: %s", line)
		}

		zones := strings.Split(fields[1], "&")
		if len(zones) < 1 {
			log.Fatalf("Invalid format in timezone file: %s", line)
		}

		// parse our prefix
		prefix, err := strconv.ParseInt(fields[0], 10, 32)
		if err != nil {
			log.Fatalf("Invalid prefix in line: %s", line)
		}
		prefixMap[int32(prefix)] = zones
	}

	// then write our file
	writeIntStringArrayMap(tzPath, tzVar, prefixMap)
}

func writeIntStringArrayMap(path string, varName string, prefixMap map[int32][]string) {
	// build lists of our keys and values
	keys := make([]int, 0, len(prefixMap))
	values := make([]string, 0, 255)
	seenValues := make(map[string]bool, 255)

	for k, vs := range prefixMap {
		keys = append(keys, int(k))
		for _, v := range vs {
			_, seen := seenValues[v]
			if !seen {
				seenValues[v] = true
				values = append(values, v)
			}
		}
	}
	sort.Strings(values)
	sort.Ints(keys)

	internMap := make(map[string]int, len(values))
	for i, v := range values {
		internMap[v] = i
	}

	data := &bytes.Buffer{}

	// first write our values, as length of string and raw bytes
	joinedValues := strings.Join(values, "\n")
	if err := binary.Write(data, binary.LittleEndian, uint32(len(joinedValues))); err != nil {
		log.Fatal(err)
	}
	if err := binary.Write(data, binary.LittleEndian, []byte(joinedValues)); err != nil {
		log.Fatal(err)
	}

	// then the number of keys
	if err := binary.Write(data, binary.LittleEndian, uint32(len(keys))); err != nil {
		log.Fatal(err)
	}

	// we write our key / value pairs as a varint of the difference of the previous prefix
	// and a uint16 of the value index
	last := 0
	intBuf := make([]byte, 6)
	for _, key := range keys {
		// first write our prefix
		diff := key - last
		l := binary.PutUvarint(intBuf, uint64(diff))
		if err := binary.Write(data, binary.LittleEndian, intBuf[:l]); err != nil {
			log.Fatal(err)
		}

		// then our values
		values := prefixMap[int32(key)]

		// write our number of values
		if err := binary.Write(data, binary.LittleEndian, uint8(len(values))); err != nil {
			log.Fatal(err)
		}

		// then each value as the interned index
		for _, v := range values {
			valueIntern := internMap[v]
			if err := binary.Write(data, binary.LittleEndian, uint16(valueIntern)); err != nil {
				log.Fatal(err)
			}
		}

		last = key
	}

	// then write our file
	writeFile(path, generateBinFile(varName, data.Bytes()))
}

func buildMetadata() *phonenumbers.PhoneMetadataCollection {
	log.Println("Fetching PhoneNumberMetadata.xml from Github")
	body := fetchURL(metadataURL)

	log.Println("Building new metadata collection")
	collection, err := phonenumbers.BuildPhoneMetadataCollection(body, false, false, false)
	if err != nil {
		log.Fatalf("Error converting XML: %s", err)
	}

	// write it out as a protobuf
	data, err := proto.Marshal(collection)
	if err != nil {
		log.Fatalf("Error marshalling metadata: %v", err)
	}

	log.Println("Writing new metadata_bin.go")
	writeFile(metadataPath, generateBinFile("metadataData", data))
	return collection
}

func buildShortNumberMetadata() *phonenumbers.PhoneMetadataCollection {
	log.Println("Fetching ShortNumberMetadata.xml from Github")
	body := fetchURL(shortNumberMetadataURL)

	log.Println("Building new short number metadata collection")
	collection, err := phonenumbers.BuildPhoneMetadataCollection(body, false, false, true)
	if err != nil {
		log.Fatalf("Error converting XML: %s", err)
	}

	// write it out as a protobuf
	data, err := proto.Marshal(collection)
	if err != nil {
		log.Fatalf("Error marshalling metadata: %v", err)
	}

	log.Println("Writing new metadata_bin.go")
	writeFile(shortNumberMetadataPath, generateBinFile("shortNumberMetadataData", data))
	return collection
}

// generates the file contents for a data file
func generateBinFile(variableName string, data []byte) []byte {
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	w.Write(data)
	w.Close()
	encoded := base64.StdEncoding.EncodeToString(compressed.Bytes())

	// create our output
	output := &bytes.Buffer{}

	// write our header
	output.WriteString("package phonenumbers\n\nvar ")
	output.WriteString(variableName)
	output.WriteString(" = ")
	output.WriteString(strconv.Quote(string(encoded)))
	output.WriteString("\n")
	return output.Bytes()
}

func buildPrefixData(build *prefixBuild) {
	log.Println("Fetching " + build.url + " from Github")
	svnExport(build.dir, build.url)

	// get our top level language directories
	dirs, err := filepath.Glob(build.dir + "/*")
	if err != nil {
		log.Fatal(err)
	}

	// for each directory
	languageMappings := make(map[string]map[int32]string)
	for _, dir := range dirs {
		// only look at directories
		fi, _ := os.Stat(dir)
		if !fi.IsDir() {
			log.Printf("Ignoring directory: %s\n", dir)
			continue
		}

		// get our language code
		parts := strings.Split(dir, "/")

		// build a map for that directory
		mappings := readMappingsForDir(dir)

		// save it for our language
		languageMappings[parts[1]] = mappings
	}

	output := bytes.Buffer{}
	output.WriteString("package phonenumbers\n\n")
	output.WriteString(fmt.Sprintf("var %s = map[string]string {\n", build.varName))

	for lang, mappings := range languageMappings {
		// iterate through our map, creating our full set of values and prefixes
		prefixes := make([]int, 0, len(mappings))
		seenValues := make(map[string]bool)
		values := make([]string, 0, 255)
		for prefix, value := range mappings {
			prefixes = append(prefixes, int(prefix))
			_, seen := seenValues[value]
			if !seen {
				values = append(values, value)
				seenValues[value] = true
			}
		}

		// make sure we won't overrun uint16s
		if len(values) > math.MaxUint16 {
			log.Fatal("too many values to represent in uint16")
		}

		// need sorted prefixes for our diff writing to work
		sort.Ints(prefixes)

		// sorted values compress better
		sort.Strings(values)

		// build our reverse mapping from value to offset
		internMappings := make(map[string]uint16)
		for i, value := range values {
			internMappings[value] = uint16(i)
		}

		// write our map
		data := &bytes.Buffer{}

		// first write our values, as length of string and raw bytes
		joinedValues := strings.Join(values, "\n")
		if err = binary.Write(data, binary.LittleEndian, uint32(len(joinedValues))); err != nil {
			log.Fatal(err)
		}
		if err = binary.Write(data, binary.LittleEndian, []byte(joinedValues)); err != nil {
			log.Fatal(err)
		}

		// then then number of prefix / value pairs
		if err = binary.Write(data, binary.LittleEndian, uint32(len(prefixes))); err != nil {
			log.Fatal(err)
		}

		// we write our prefix / value pairs as a varint of the difference of the previous prefix
		// and a uint16 of the value index
		last := 0
		intBuf := make([]byte, 6)
		for _, prefix := range prefixes {
			value := mappings[int32(prefix)]
			valueIntern := internMappings[value]
			diff := prefix - last
			l := binary.PutUvarint(intBuf, uint64(diff))
			if err = binary.Write(data, binary.LittleEndian, intBuf[:l]); err != nil {
				log.Fatal(err)
			}
			if err = binary.Write(data, binary.LittleEndian, uint16(valueIntern)); err != nil {
				log.Fatal(err)
			}

			last = prefix
		}

		var compressed bytes.Buffer
		w := gzip.NewWriter(&compressed)
		w.Write(data.Bytes())
		w.Close()
		c := base64.StdEncoding.EncodeToString(compressed.Bytes())
		output.WriteString("\t")
		output.WriteString(strconv.Quote(lang))
		output.WriteString(": ")
		output.WriteString(strconv.Quote(c))
		output.WriteString(",\n")
	}

	output.WriteString("}")
	writeFile(build.srcPath, output.Bytes())
}

func readMappingsForDir(dir string) map[int32]string {
	log.Printf("Building map for: %s\n", dir)
	mappings := make(map[int32]string)

	files, err := filepath.Glob(dir + "/*.txt")
	if err != nil {
		log.Fatal(err)
	}
	for _, file := range files {
		body, err := ioutil.ReadFile(file)
		if err != nil {
			log.Fatal(err)
		}
		items := strings.Split(file, "/")
		if len(items) != 3 {
			log.Fatalf("file name %s not correct", file)
		}

		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(line, "#") {
				continue
			}
			if strings.TrimSpace(line) == "" {
				continue
			}
			fields := strings.Split(line, "|")
			if len(fields) != 2 {
				continue
			}
			prefix := fields[0]
			tmp1, err := strconv.ParseInt(prefix, 10, 32)
			prefixInt := int32(tmp1)
			if err != nil || prefixInt < 0 {
				log.Fatalf("Unable to parse line: %s", line)
			}

			value := strings.TrimSpace(fields[1])
			if value == "" {
				log.Printf("Ignoring empty value: %s", line)
			}

			_, repeat := mappings[prefixInt]
			if repeat {
				log.Fatalf("Repeated prefix for line: %s", line)
			}
			mappings[prefixInt] = fields[1]
		}
	}

	log.Printf("Read %d mappings in %s\n", len(mappings), dir)
	return mappings
}

func main() {
	metadata := buildMetadata()
	buildShortNumberMetadata()
	buildRegions(metadata)
	buildTimezones()
	buildPrefixData(&carrier)
	buildPrefixData(&geocoding)
}
