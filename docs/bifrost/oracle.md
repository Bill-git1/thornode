# Oracle

## Overview

The price oracle is a component running in Bifrost and responsible for providing crypto asset prices to Thornode, so they can be consumed by applications on the app layer. Each node runs an instance of the oracle and reporting observed prices independently.

## Concept

### Oracle (Bifrost)

#### Price observation

The Oracle component is running in Bifrost and consists of a multitude of different price providers for different centralized exchanges. These providers either subscribe to websocket feeds or poll API endpoints in a specified interval.

Every second, the oracle collects all rates of all configured trading pairs from all enabled providers. It then calculates the USD value of each base asset and filters them for outliers.

#### Calculating USD value

The oracle tries to calculate the USD rates for every asset by "traversing" the trading pairs from USD to base asset. That means, for the trading pair `BTC/USDT`, it first collects all available `USDT/USD` rates and then computes the USD value for pairs with USDT as quote asset.

That means, that the oracle needs enough assets paired with USD to begin with. It will, for example, use `BTC/USD` to calculate the `USDT/USD` rate from `BTC/USDT`, if it has no, or not enough `USDT/USD` rates collected.

#### Outlier detection

Before sending the USD rates for every asset, the oracle also checks for outliers, in case one of the providers reports prices. It is doing that by calculating the median absolute deviation of all prices for each asset and removes all prices outside the set boundaries.

#### Volume weighted average

All remaining USD rates for every asset are averaged into a single value weighted by the 24h trading volume, reported by the provider.

### P2P Gossip (Bifrost)

#### Broadcast price feeds

To get the most recent prices into Thorchain, the oracle broadcasts the final USD rates, as soon as it finishes its computation. This is currently set to 1s.

#### Attestation handling

Price feeds use the same gossip mechanism like observed L1 transactions or fee updates, but with a twist: Because each node likely observes slightly different prices (due to latency to providers, different configured providers etc...), nodes can't verify or attest the rates they received from their peers. They only verify the signature of the feeds and forward them to their Thornode instance.

#### Timestamps

To avoid replacing a recent price feed with an older one, each feed message provides a timestamp of when it is sent by the oracle. Older messages are simply discarded.

#### Signatures

Each price feed payload is signed by the sending node and and indexed by its public key. This way it is guaranteed, that the price data can't be manipulated or forged by other nodes.

#### Batching

To reduce calls to Thornode but allow for fast updates (multiple times per second), new price feeds are grouped into a `MsgPriceFeedsBatch` and executed with a 100ms delay. So the gRPC endpoint is hit less than 10 times per second instead of 120 times.

### Enshrined Bifrost (Thornode)

#### Processing

The final calculation of the on chain price is done in the message handler of `MsgPriceFeedsBatch`. This message contains all observed price feeds of every node up to this point. It is a single batch transaction to prevent reorder attacks. On every `BeginBlock` all oracle prices are removed. Should a node try to execute a message before the price feed transaction, there are no oracle prices and the transaction fails.

#### Final on-chain price

To calculate the final on-chain prices, Thornode first checks the signature and timestamp of each provided price feed. If a feed is older than one block or has an invalid signature, it is discarded. The resulting price for each asset is the median of all provided prices for that asset.

##### Stale price feeds

Price feeds that are older than the timestamp of the last block are discarded as well. This is to prevent a malicious node from storing old prices with valid signatures and use them for price manipulation.

##### Simple majority

If a simple majority of nodes have provided a valid price for an asset, this price is persisted to the KVStore and can be consumed inside the current block.

##### Multiple nodes

For node operators that are running multiple nodes, it is sufficient if a single valid price feed of one of his nodes is found. For nodes where one or more prices are missing, the last found price of another node with the same node operator address is used.

## Configuration

The price oracle itself has no special configuration besides logging verbosity, which defaults to `info` and an option to disable it completely. It is disabled by default.

```golang
type BifrostOracleConfiguration struct {
    LogLevel string `mapstructure:"log_level"`
    Disabled bool   `mapstructure:"disabled"`
}
```

### Providers

Price providers can be configured independently and are enabled by default, to collect as many price feeds from as many different sources as possible.

Nodes can change the configuration by providing the corresponding environment variable:

```sh
BIFROST_PROVIDERS_COINBASE_DISABLED="false"
```

All rest api and websockets URLs, as well as trading pairs are predefined in `default.yaml`:

```yaml
coinbase:
  name: coinbase
  disabled: true
  polling_interval: 10s
  api_endpoints:
    - https://api.exchange.coinbase.com
  ws_endpoints:
    - wss://ws-feed.exchange.coinbase.com
  pairs:
    - ATOM/USD
    - AVAX/USD
    - BCH/USD
    - BTC/USD
    - DOGE/USD
    - ETH/USD
    - LTC/USD
    - SOL/USD
    - USDT/USD
    - XRP/USD
  symbol_mapping: []
```

Disabling all providers, also disables the oracle (technically it just doesn't run)

> [!important]
> Note: Certain providers will not be available from specific jurisdictions

```golang
type BifrostOracleProviderConfiguration struct {
	Name            string        `mapstructure:"name"`
	Disabled        bool          `mapstructure:"disabled"`
	PollingInterval time.Duration `mapstructure:"polling_interval"`
	ApiEndpoints    []string      `mapstructure:"api_endpoints"`
	WsEndpoints     []string      `mapstructure:"ws_endpoints"`
	Pairs           []string      `mapstructure:"pairs"`
	SymbolMapping   []string      `mapstructure:"symbol_mapping"`
}
```

## Metrics

The price oracle provides metrics to the collected ticker prices and calculations, which can be accessed via the prometheus endpoint:

### Bifrost

| Name                          | Notes                                                                  |
| ----------------------------- | ---------------------------------------------------------------------- |
| oracle_provider_bounds        | Upper- and lower bounds for outlier detection                          |
| oracle_provider_price         | Final asset price in USD for each provider                             |
| oracle_provider_rate          | Reported rate for each trading pair and provider                       |
| oracle_provider_updates_total | Counter of ticker updates                                              |
| oracle_provider_volume        | Reported 24h volume for each trading pair and provider (in base asset) |

### Thornode

| Name                         | Notes                                                        |
| ---------------------------- | ------------------------------------------------------------ |
| thornode_oracle_price_bounds | Upper- and lower bounds for outlier detection (not used yet) |
| thornode_oracle_price        | Final on chain price for any asset                           |

### Detailed Price Metrics

Detailed price metrics are disabled by default but can be enabled separately for each component.

#### Provider prices

Detailed metrics in Bifrost provide 24h trading volume and exchange rates of every observed trading pair on every provider, as well as the resulting USD rate for every provider and volume/weightings used for the calculation.

```sh
BIFROST_ORACLE_DETAILED_METRICS="true"
```

#### Prices by node

Detailed metrics in Thornode track the submitted USD price for each asset of each node, updated on every block.

```sh
THOR_TELEMETRY_PRICE_PER_NODE="true"
```

## Mimirs

There are two operational Mimirs available for the Oracle

### HaltOracle

HaltOracle will stop the oracle from sending price feeds via p2p gossip. The oracle in Bifrost itself is still running and receiving ticker prices to be able to immediately send feeds once HaltOracle is set to a value <= 0.

### OracleUpdateInterval

OracleUpdateInterval sets the interval in milliseconds in which the oracle calculates and sends the USD values of all configured assets via p2p gossip. If no mimir value is set, it uses the default value of 1 second.
