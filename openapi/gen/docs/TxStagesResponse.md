# TxStagesResponse

## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**InboundObserved** | [**TxStagesResponseInboundObserved**](TxStagesResponseInboundObserved.md) |  | 
**InboundConfirmationCounted** | Pointer to [**TxStagesResponseInboundConfirmationCounted**](TxStagesResponseInboundConfirmationCounted.md) |  | [optional] 
**InboundFinalised** | Pointer to [**TxStagesResponseInboundFinalised**](TxStagesResponseInboundFinalised.md) |  | [optional] 
**SwapStatus** | Pointer to [**TxStagesResponseSwapStatus**](TxStagesResponseSwapStatus.md) |  | [optional] 
**SwapFinalised** | Pointer to [**TxStagesResponseSwapFinalised**](TxStagesResponseSwapFinalised.md) |  | [optional] 
**OutboundDelay** | Pointer to [**TxStagesResponseOutboundDelay**](TxStagesResponseOutboundDelay.md) |  | [optional] 
**OutboundSigned** | Pointer to [**TxStagesResponseOutboundSigned**](TxStagesResponseOutboundSigned.md) |  | [optional] 

## Methods

### NewTxStagesResponse

`func NewTxStagesResponse(inboundObserved TxStagesResponseInboundObserved, ) *TxStagesResponse`

NewTxStagesResponse instantiates a new TxStagesResponse object
This constructor will assign default values to properties that have it defined,
and makes sure properties required by API are set, but the set of arguments
will change when the set of required properties is changed

### NewTxStagesResponseWithDefaults

`func NewTxStagesResponseWithDefaults() *TxStagesResponse`

NewTxStagesResponseWithDefaults instantiates a new TxStagesResponse object
This constructor will only assign default values to properties that have it defined,
but it doesn't guarantee that properties required by API are set

### GetInboundObserved

`func (o *TxStagesResponse) GetInboundObserved() TxStagesResponseInboundObserved`

GetInboundObserved returns the InboundObserved field if non-nil, zero value otherwise.

### GetInboundObservedOk

`func (o *TxStagesResponse) GetInboundObservedOk() (*TxStagesResponseInboundObserved, bool)`

GetInboundObservedOk returns a tuple with the InboundObserved field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetInboundObserved

`func (o *TxStagesResponse) SetInboundObserved(v TxStagesResponseInboundObserved)`

SetInboundObserved sets InboundObserved field to given value.


### GetInboundConfirmationCounted

`func (o *TxStagesResponse) GetInboundConfirmationCounted() TxStagesResponseInboundConfirmationCounted`

GetInboundConfirmationCounted returns the InboundConfirmationCounted field if non-nil, zero value otherwise.

### GetInboundConfirmationCountedOk

`func (o *TxStagesResponse) GetInboundConfirmationCountedOk() (*TxStagesResponseInboundConfirmationCounted, bool)`

GetInboundConfirmationCountedOk returns a tuple with the InboundConfirmationCounted field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetInboundConfirmationCounted

`func (o *TxStagesResponse) SetInboundConfirmationCounted(v TxStagesResponseInboundConfirmationCounted)`

SetInboundConfirmationCounted sets InboundConfirmationCounted field to given value.

### HasInboundConfirmationCounted

`func (o *TxStagesResponse) HasInboundConfirmationCounted() bool`

HasInboundConfirmationCounted returns a boolean if a field has been set.

### GetInboundFinalised

`func (o *TxStagesResponse) GetInboundFinalised() TxStagesResponseInboundFinalised`

GetInboundFinalised returns the InboundFinalised field if non-nil, zero value otherwise.

### GetInboundFinalisedOk

`func (o *TxStagesResponse) GetInboundFinalisedOk() (*TxStagesResponseInboundFinalised, bool)`

GetInboundFinalisedOk returns a tuple with the InboundFinalised field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetInboundFinalised

`func (o *TxStagesResponse) SetInboundFinalised(v TxStagesResponseInboundFinalised)`

SetInboundFinalised sets InboundFinalised field to given value.

### HasInboundFinalised

`func (o *TxStagesResponse) HasInboundFinalised() bool`

HasInboundFinalised returns a boolean if a field has been set.

### GetSwapStatus

`func (o *TxStagesResponse) GetSwapStatus() TxStagesResponseSwapStatus`

GetSwapStatus returns the SwapStatus field if non-nil, zero value otherwise.

### GetSwapStatusOk

`func (o *TxStagesResponse) GetSwapStatusOk() (*TxStagesResponseSwapStatus, bool)`

GetSwapStatusOk returns a tuple with the SwapStatus field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetSwapStatus

`func (o *TxStagesResponse) SetSwapStatus(v TxStagesResponseSwapStatus)`

SetSwapStatus sets SwapStatus field to given value.

### HasSwapStatus

`func (o *TxStagesResponse) HasSwapStatus() bool`

HasSwapStatus returns a boolean if a field has been set.

### GetSwapFinalised

`func (o *TxStagesResponse) GetSwapFinalised() TxStagesResponseSwapFinalised`

GetSwapFinalised returns the SwapFinalised field if non-nil, zero value otherwise.

### GetSwapFinalisedOk

`func (o *TxStagesResponse) GetSwapFinalisedOk() (*TxStagesResponseSwapFinalised, bool)`

GetSwapFinalisedOk returns a tuple with the SwapFinalised field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetSwapFinalised

`func (o *TxStagesResponse) SetSwapFinalised(v TxStagesResponseSwapFinalised)`

SetSwapFinalised sets SwapFinalised field to given value.

### HasSwapFinalised

`func (o *TxStagesResponse) HasSwapFinalised() bool`

HasSwapFinalised returns a boolean if a field has been set.

### GetOutboundDelay

`func (o *TxStagesResponse) GetOutboundDelay() TxStagesResponseOutboundDelay`

GetOutboundDelay returns the OutboundDelay field if non-nil, zero value otherwise.

### GetOutboundDelayOk

`func (o *TxStagesResponse) GetOutboundDelayOk() (*TxStagesResponseOutboundDelay, bool)`

GetOutboundDelayOk returns a tuple with the OutboundDelay field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetOutboundDelay

`func (o *TxStagesResponse) SetOutboundDelay(v TxStagesResponseOutboundDelay)`

SetOutboundDelay sets OutboundDelay field to given value.

### HasOutboundDelay

`func (o *TxStagesResponse) HasOutboundDelay() bool`

HasOutboundDelay returns a boolean if a field has been set.

### GetOutboundSigned

`func (o *TxStagesResponse) GetOutboundSigned() TxStagesResponseOutboundSigned`

GetOutboundSigned returns the OutboundSigned field if non-nil, zero value otherwise.

### GetOutboundSignedOk

`func (o *TxStagesResponse) GetOutboundSignedOk() (*TxStagesResponseOutboundSigned, bool)`

GetOutboundSignedOk returns a tuple with the OutboundSigned field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetOutboundSigned

`func (o *TxStagesResponse) SetOutboundSigned(v TxStagesResponseOutboundSigned)`

SetOutboundSigned sets OutboundSigned field to given value.

### HasOutboundSigned

`func (o *TxStagesResponse) HasOutboundSigned() bool`

HasOutboundSigned returns a boolean if a field has been set.


[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


