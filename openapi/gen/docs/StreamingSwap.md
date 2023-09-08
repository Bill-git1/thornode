# StreamingSwap

## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**TxId** | Pointer to **string** | the hash of a transaction | [optional] 
**Interval** | Pointer to **int32** | how often each swap is made, in blocks | [optional] 
**Quantity** | Pointer to **int32** | the total number of swaps in a streaming swaps | [optional] 
**Count** | Pointer to **int32** | the amount of swap attempts so far | [optional] 
**LastHeight** | Pointer to **int64** | the block height of the latest swap | [optional] 
**TradeTarget** | Pointer to **string** | the total number of tokens the swapper wants to receive of the output asset | [optional] 
**Deposit** | Pointer to **string** | the number of input tokens the swapper has deposited | [optional] 
**In** | Pointer to **string** | the amount of input tokens that have been swapped so far | [optional] 
**Out** | Pointer to **string** | the amount of output tokens that have been swapped so far | [optional] 
**FailedSwaps** | Pointer to **[]int32** | the list of swap indexes that failed | [optional] 
**FailedSwapReasons** | Pointer to **[]string** | the list of reasons that sub-swaps have failed | [optional] 

## Methods

### NewStreamingSwap

`func NewStreamingSwap() *StreamingSwap`

NewStreamingSwap instantiates a new StreamingSwap object
This constructor will assign default values to properties that have it defined,
and makes sure properties required by API are set, but the set of arguments
will change when the set of required properties is changed

### NewStreamingSwapWithDefaults

`func NewStreamingSwapWithDefaults() *StreamingSwap`

NewStreamingSwapWithDefaults instantiates a new StreamingSwap object
This constructor will only assign default values to properties that have it defined,
but it doesn't guarantee that properties required by API are set

### GetTxId

`func (o *StreamingSwap) GetTxId() string`

GetTxId returns the TxId field if non-nil, zero value otherwise.

### GetTxIdOk

`func (o *StreamingSwap) GetTxIdOk() (*string, bool)`

GetTxIdOk returns a tuple with the TxId field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetTxId

`func (o *StreamingSwap) SetTxId(v string)`

SetTxId sets TxId field to given value.

### HasTxId

`func (o *StreamingSwap) HasTxId() bool`

HasTxId returns a boolean if a field has been set.

### GetInterval

`func (o *StreamingSwap) GetInterval() int32`

GetInterval returns the Interval field if non-nil, zero value otherwise.

### GetIntervalOk

`func (o *StreamingSwap) GetIntervalOk() (*int32, bool)`

GetIntervalOk returns a tuple with the Interval field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetInterval

`func (o *StreamingSwap) SetInterval(v int32)`

SetInterval sets Interval field to given value.

### HasInterval

`func (o *StreamingSwap) HasInterval() bool`

HasInterval returns a boolean if a field has been set.

### GetQuantity

`func (o *StreamingSwap) GetQuantity() int32`

GetQuantity returns the Quantity field if non-nil, zero value otherwise.

### GetQuantityOk

`func (o *StreamingSwap) GetQuantityOk() (*int32, bool)`

GetQuantityOk returns a tuple with the Quantity field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetQuantity

`func (o *StreamingSwap) SetQuantity(v int32)`

SetQuantity sets Quantity field to given value.

### HasQuantity

`func (o *StreamingSwap) HasQuantity() bool`

HasQuantity returns a boolean if a field has been set.

### GetCount

`func (o *StreamingSwap) GetCount() int32`

GetCount returns the Count field if non-nil, zero value otherwise.

### GetCountOk

`func (o *StreamingSwap) GetCountOk() (*int32, bool)`

GetCountOk returns a tuple with the Count field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetCount

`func (o *StreamingSwap) SetCount(v int32)`

SetCount sets Count field to given value.

### HasCount

`func (o *StreamingSwap) HasCount() bool`

HasCount returns a boolean if a field has been set.

### GetLastHeight

`func (o *StreamingSwap) GetLastHeight() int64`

GetLastHeight returns the LastHeight field if non-nil, zero value otherwise.

### GetLastHeightOk

`func (o *StreamingSwap) GetLastHeightOk() (*int64, bool)`

GetLastHeightOk returns a tuple with the LastHeight field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetLastHeight

`func (o *StreamingSwap) SetLastHeight(v int64)`

SetLastHeight sets LastHeight field to given value.

### HasLastHeight

`func (o *StreamingSwap) HasLastHeight() bool`

HasLastHeight returns a boolean if a field has been set.

### GetTradeTarget

`func (o *StreamingSwap) GetTradeTarget() string`

GetTradeTarget returns the TradeTarget field if non-nil, zero value otherwise.

### GetTradeTargetOk

`func (o *StreamingSwap) GetTradeTargetOk() (*string, bool)`

GetTradeTargetOk returns a tuple with the TradeTarget field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetTradeTarget

`func (o *StreamingSwap) SetTradeTarget(v string)`

SetTradeTarget sets TradeTarget field to given value.

### HasTradeTarget

`func (o *StreamingSwap) HasTradeTarget() bool`

HasTradeTarget returns a boolean if a field has been set.

### GetDeposit

`func (o *StreamingSwap) GetDeposit() string`

GetDeposit returns the Deposit field if non-nil, zero value otherwise.

### GetDepositOk

`func (o *StreamingSwap) GetDepositOk() (*string, bool)`

GetDepositOk returns a tuple with the Deposit field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetDeposit

`func (o *StreamingSwap) SetDeposit(v string)`

SetDeposit sets Deposit field to given value.

### HasDeposit

`func (o *StreamingSwap) HasDeposit() bool`

HasDeposit returns a boolean if a field has been set.

### GetIn

`func (o *StreamingSwap) GetIn() string`

GetIn returns the In field if non-nil, zero value otherwise.

### GetInOk

`func (o *StreamingSwap) GetInOk() (*string, bool)`

GetInOk returns a tuple with the In field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetIn

`func (o *StreamingSwap) SetIn(v string)`

SetIn sets In field to given value.

### HasIn

`func (o *StreamingSwap) HasIn() bool`

HasIn returns a boolean if a field has been set.

### GetOut

`func (o *StreamingSwap) GetOut() string`

GetOut returns the Out field if non-nil, zero value otherwise.

### GetOutOk

`func (o *StreamingSwap) GetOutOk() (*string, bool)`

GetOutOk returns a tuple with the Out field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetOut

`func (o *StreamingSwap) SetOut(v string)`

SetOut sets Out field to given value.

### HasOut

`func (o *StreamingSwap) HasOut() bool`

HasOut returns a boolean if a field has been set.

### GetFailedSwaps

`func (o *StreamingSwap) GetFailedSwaps() []int32`

GetFailedSwaps returns the FailedSwaps field if non-nil, zero value otherwise.

### GetFailedSwapsOk

`func (o *StreamingSwap) GetFailedSwapsOk() (*[]int32, bool)`

GetFailedSwapsOk returns a tuple with the FailedSwaps field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetFailedSwaps

`func (o *StreamingSwap) SetFailedSwaps(v []int32)`

SetFailedSwaps sets FailedSwaps field to given value.

### HasFailedSwaps

`func (o *StreamingSwap) HasFailedSwaps() bool`

HasFailedSwaps returns a boolean if a field has been set.

### GetFailedSwapReasons

`func (o *StreamingSwap) GetFailedSwapReasons() []string`

GetFailedSwapReasons returns the FailedSwapReasons field if non-nil, zero value otherwise.

### GetFailedSwapReasonsOk

`func (o *StreamingSwap) GetFailedSwapReasonsOk() (*[]string, bool)`

GetFailedSwapReasonsOk returns a tuple with the FailedSwapReasons field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetFailedSwapReasons

`func (o *StreamingSwap) SetFailedSwapReasons(v []string)`

SetFailedSwapReasons sets FailedSwapReasons field to given value.

### HasFailedSwapReasons

`func (o *StreamingSwap) HasFailedSwapReasons() bool`

HasFailedSwapReasons returns a boolean if a field has been set.


[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


