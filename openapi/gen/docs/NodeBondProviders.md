# NodeBondProviders

## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**NodeOperatorFee** | Pointer to **string** | node operator fee in basis points | [optional] 
**Providers** | Pointer to [**NodeBondProvider**](NodeBondProvider.md) |  | [optional] 

## Methods

### NewNodeBondProviders

`func NewNodeBondProviders() *NodeBondProviders`

NewNodeBondProviders instantiates a new NodeBondProviders object
This constructor will assign default values to properties that have it defined,
and makes sure properties required by API are set, but the set of arguments
will change when the set of required properties is changed

### NewNodeBondProvidersWithDefaults

`func NewNodeBondProvidersWithDefaults() *NodeBondProviders`

NewNodeBondProvidersWithDefaults instantiates a new NodeBondProviders object
This constructor will only assign default values to properties that have it defined,
but it doesn't guarantee that properties required by API are set

### GetNodeOperatorFee

`func (o *NodeBondProviders) GetNodeOperatorFee() string`

GetNodeOperatorFee returns the NodeOperatorFee field if non-nil, zero value otherwise.

### GetNodeOperatorFeeOk

`func (o *NodeBondProviders) GetNodeOperatorFeeOk() (*string, bool)`

GetNodeOperatorFeeOk returns a tuple with the NodeOperatorFee field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetNodeOperatorFee

`func (o *NodeBondProviders) SetNodeOperatorFee(v string)`

SetNodeOperatorFee sets NodeOperatorFee field to given value.

### HasNodeOperatorFee

`func (o *NodeBondProviders) HasNodeOperatorFee() bool`

HasNodeOperatorFee returns a boolean if a field has been set.

### GetProviders

`func (o *NodeBondProviders) GetProviders() NodeBondProvider`

GetProviders returns the Providers field if non-nil, zero value otherwise.

### GetProvidersOk

`func (o *NodeBondProviders) GetProvidersOk() (*NodeBondProvider, bool)`

GetProvidersOk returns a tuple with the Providers field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetProviders

`func (o *NodeBondProviders) SetProviders(v NodeBondProvider)`

SetProviders sets Providers field to given value.

### HasProviders

`func (o *NodeBondProviders) HasProviders() bool`

HasProviders returns a boolean if a field has been set.


[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


