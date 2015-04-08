package swagger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func testJsonFromStruct(t *testing.T, sample interface{}, expectedJson string) bool {
	m := modelsFromStruct(sample)
	data, _ := json.MarshalIndent(m, " ", " ")
	return compareJson(t, string(data), expectedJson)
}

func modelsFromStruct(sample interface{}) map[string]Model {
	models := map[string]Model{}
	builder := modelBuilder{models}
	builder.addModel(reflect.TypeOf(sample), "")
	return models
}

func compareJson(t *testing.T, actualJsonAsString string, expectedJsonAsString string) bool {
	var actualMap map[string]interface{}
	json.Unmarshal([]byte(actualJsonAsString), &actualMap)
	var expectedMap map[string]interface{}
	json.Unmarshal([]byte(expectedJsonAsString), &expectedMap)
	if !reflect.DeepEqual(actualMap, expectedMap) {
		fmt.Println("---- expected -----")
		fmt.Println(withLineNumbers(expectedJsonAsString))
		fmt.Println("---- actual -----")
		fmt.Println(withLineNumbers(actualJsonAsString))
		fmt.Println("---- raw -----")
		fmt.Println(actualJsonAsString)
		t.Error("there are differences")
		return false
	}
	return true
}

func indexOfNonMatchingLine(actual, expected string) int {
	a := strings.Split(actual, "\n")
	e := strings.Split(expected, "\n")
	size := len(a)
	if len(e) < len(a) {
		size = len(e)
	}
	for i := 0; i < size; i++ {
		if a[i] != e[i] {
			return i
		}
	}
	return -1
}

func withLineNumbers(content string) string {
	var buffer bytes.Buffer
	lines := strings.Split(content, "\n")
	for i, each := range lines {
		buffer.WriteString(fmt.Sprintf("%d:%s\n", i, each))
	}
	return buffer.String()
}
