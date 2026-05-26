package openapi

// The openapi package contains generated types based on the OpenAPI spec. These types
// are leveraged in the thornode querier handlers where applicable, but many of the
// querier responses leverage existing types generated from protobuf definitions. In
// these cases we add tests to ensure that the generated types from the API spec should
// at least have matching struct tags with those from the types used in the querier
// responses to ensure the API spec is accurate and can be used to generate clients.

import (
	"reflect"
	"testing"

	"gitlab.com/thorchain/thornode/v3/common"
	gen "gitlab.com/thorchain/thornode/v3/openapi/gen"
	types "gitlab.com/thorchain/thornode/v3/x/thorchain/types"

	. "gopkg.in/check.v1"
	"gopkg.in/yaml.v2"
)

// -------------------------------------------------------------------------------------
// Init
// -------------------------------------------------------------------------------------

func TestPackage(t *testing.T) { TestingT(t) }

type Test struct{}

var _ = Suite(&Test{})

// -------------------------------------------------------------------------------------
// Tests
// -------------------------------------------------------------------------------------

func (Test) TestJSONSpec(c *C) {
	// common
	assertJSONStructTagsMatch(c, common.Coin{}, gen.Coin{})
	assertJSONStructTagsMatch(c, common.Tx{}, gen.Tx{})

	// queue and lp
	assertJSONStructTagsMatch(c, types.MsgSwap{}, gen.MsgSwap{})

	// txs
	assertJSONStructTagsMatch(c, types.TxOut{}, gen.KeysignInfo{})
	// TODO: Check that TxOutItem struct tags match
	// if or when the THORNode struct includes its (scheduled) Height field.
	// assertJSONStructTagsMatch(c, types.TxOutItem{}, gen.TxOutItem{})

	// tss
	assertJSONStructTagsMatch(c, types.NodeTssTime{}, gen.NodeKeygenMetric{})
	assertJSONStructTagsMatch(c, types.TssKeygenMetric{}, gen.KeygenMetric{})
	assertJSONStructTagsMatch(c, types.TssKeysignMetric{}, gen.TssKeysignMetric{})

	// miscellaneous
	assertJSONStructTagsMatch(c, types.BanVoter{}, gen.BanResponse{})
}

func (Test) TestSwapQueueOpenAPIContract(c *C) {
	spec := map[interface{}]interface{}{}
	c.Assert(yaml.Unmarshal(openapiYAML, &spec), IsNil)

	legacySchema := responseSchema(c, spec, "/thorchain/queue/swap")
	c.Assert(legacySchema["$ref"], Equals, "#/components/schemas/SwapQueueResponse")

	paginatedSchema := responseSchema(c, spec, "/thorchain/queue/swap/paginated")
	c.Assert(paginatedSchema["$ref"], Equals, "#/components/schemas/SwapQueuePaginatedResponse")

	components := yamlMap(c, spec["components"])
	schemas := yamlMap(c, components["schemas"])

	swapQueueResponse := yamlMap(c, schemas["SwapQueueResponse"])
	c.Assert(swapQueueResponse["type"], Equals, "array")
	c.Assert(swapQueueResponse["items"], NotNil)

	paginatedResponse := yamlMap(c, schemas["SwapQueuePaginatedResponse"])
	c.Assert(paginatedResponse["type"], Equals, "object")
	paginatedProperties := yamlMap(c, paginatedResponse["properties"])
	c.Assert(paginatedProperties["swap_queue"], NotNil)
	c.Assert(paginatedProperties["pagination"], NotNil)
}

// -------------------------------------------------------------------------------------
// Helpers
// -------------------------------------------------------------------------------------

func assertJSONStructTagsMatch(c *C, thor, spec interface{}) {
	thorType := reflect.TypeOf(thor)
	specType := reflect.TypeOf(spec)
	comment := Commentf("thorType=%s; specType=%s", thorType.Name(), specType.Name())

	c.Assert(specType.NumField(), Equals, thorType.NumField(), comment)
	for i := 0; i < thorType.NumField(); i++ {
		specTag := specType.Field(i).Tag.Get("json")
		thorTag := thorType.Field(i).Tag.Get("json")
		c.Assert(specTag, Equals, thorTag, comment)
	}
}

func responseSchema(c *C, spec map[interface{}]interface{}, pathName string) map[interface{}]interface{} {
	paths := yamlMap(c, spec["paths"])
	path := yamlMap(c, paths[pathName])
	get := yamlMap(c, path["get"])
	responses := yamlMap(c, get["responses"])
	okResponse := yamlMap(c, yamlValue(c, responses, 200))
	content := yamlMap(c, okResponse["content"])
	jsonContent := yamlMap(c, content["application/json"])
	return yamlMap(c, jsonContent["schema"])
}

func yamlMap(c *C, value interface{}) map[interface{}]interface{} {
	result, ok := value.(map[interface{}]interface{})
	c.Assert(ok, Equals, true, Commentf("value has type %T", value))
	return result
}

func yamlValue(c *C, values map[interface{}]interface{}, key interface{}) interface{} {
	value, ok := values[key]
	if !ok {
		value, ok = values["200"]
	}
	c.Assert(ok, Equals, true, Commentf("missing yaml key %v", key))
	return value
}
