# Tutorials

## **Find Liquidity Position**

Similar to savers, looking up the liquidity position with a given address is possible.

### **Liquidity Provider Position using Thornode**

**Request**: _Get liquidity provider information in the BTC pool for the address_ bc1q00nrswtpp3zddgc0uvppuszhnr8k8zfcdps9gn [https://gateway.liquify.com/chain/thorchain_api/thorchain/pool/BTC.BTC/liquidity_provider/bc1q00nrswtpp3zddgc0uvppuszhnr8k8zfcdps9gn](https://gateway.liquify.com/chain/thorchain_api/thorchain/pool/BTC.BTC/liquidity_provider/bc1q00nrswtpp3zddgc0uvppuszhnr8k8zfcdps9gn)

**Response:**

```json
{
  "asset": "BTC.BTC",
  "asset_address": "bc1q00nrswtpp3zddgc0uvppuszhnr8k8zfcdps9gn",
  "last_add_height": 6332320,
  "units": "3190637579",
  "pending_rune": "0",
  "pending_asset": "0",
  "rune_deposit_value": "5340160943",
  "asset_deposit_value": "548543",
  "rune_redeem_value": "6217698938",
  "asset_redeem_value": "500382",
  "luvi_deposit_value": "1696309",
  "luvi_redeem_value": "1748188",
  "luvi_growth_pct": "0.030583460914255598"
}
```

### **Liquidity Provider Position using Midgard**

Several endpoints exist however the member's endpoint is the most comprehensive

**Request**: Get _liquidity provider information for the address_ bc1q0kmdagyqhkzw4sgs7f0vycxw7jhexw0rl9x9as

[https://gateway.liquify.com/chain/thorchain_midgard/v2/member/thor169lfsnv2myg8yrudx4353xakq44756w9830crc](https://gateway.liquify.com/chain/thorchain_midgard/v2/member/thor169lfsnv2myg8yrudx4353xakq44756w9830crc)

**Response:**

```json
{
  "pools": [
    {
      "assetAdded": "67500000",
      "assetAddress": "bc1qw5cj49wng7jpfg2zq6ca5py7uctq4maulyc66c",
      "assetPending": "0",
      "assetWithdrawn": "0",
      "dateFirstAdded": "1669373649",
      "dateLastAdded": "1669373649",
      "liquidityUnits": "466600725237",
      "pool": "BTC.BTC",
      "runeAdded": "955003804620",
      "runeAddress": "thor169lfsnv2myg8yrudx4353xakq44756w9830crc",
      "runePending": "0",
      "runeWithdrawn": "0"
    }
    ...
  ]
}
```

Any address can be used with this endpoint, e.g. bc1q0kmdagyqhkzw4sgs7f0vycxw7jhexw0rl9x9as with `?showSavers=true` to show any savers position also.

[https://gateway.liquify.com/chain/thorchain_midgard/v2/member/bc1q0kmdagyqhkzw4sgs7f0vycxw7jhexw0rl9x9as?showSavers=true](https://gateway.liquify.com/chain/thorchain_midgard/v2/member/bc1q0kmdagyqhkzw4sgs7f0vycxw7jhexw0rl9x9as?showSavers=true)

### **User Transaction History**

Actions within THORChain can be obtained from Midgard which will list the actions taken by any given address.

**Request**: _List actions by the address bc1qn9esxuw8ca7ts8l6w66kdh800s09msvutydc46_

[https://gateway.liquify.com/chain/thorchain_midgard/v2/actions?address=bc1qn9esxuw8ca7ts8l6w66kdh800s09msvutydc46](https://gateway.liquify.com/chain/thorchain_midgard/v2/actions?address=bc1qn9esxuw8ca7ts8l6w66kdh800s09msvutydc46)

**Response:**

```json
{
  "actions": [
    {
      "date": "1647866221415353933",
      "height": "4778782",
      "in": [
        {
          "address": "thor169lfsnv2myg8yrudx4353xakq44756w9830crc",
          "coins": [
            {
              "amount": "63684757953",
              "asset": "THOR.RUNE"
            }
          ],
          "txID": "ED1384012BA129B889CCF3285A1FB73B127101A0924F49B64FE58A6939FA47C4"
        },
        {
          "address": "bc1qn9esxuw8ca7ts8l6w66kdh800s09msvutydc46",
          "coins": [
            {
              "amount": "541348102046",
              "asset": "BTC.BTC"
            }
          ],
          "txID": "F8CEAF2EA762D08AE22CC173BC4B2781B082927990C4F623D2629C4EE2BEC93F"
        }
      ],
      "metadata": {
        "addLiquidity": {
          "liquidityUnits": "38152218105"
        }
      },
      "out": [],
      "pools": [
        "BTC.BTC"
      ],
      "status": "success",
      "type": "addLiquidity"
    },
    ....
  ],
  "count": "6"
}
```

Will also include savers' actions. The Action endpoint is very flexible, see the [docs](https://gateway.liquify.com/chain/thorchain_midgard/v2/doc#operation/GetActions).

### Check the status of a Transaction

Transactions can [take time to fully process](../concepts/delays.md) once sent to THORChain.

**Request**: Get the status for BTC tx A56B423250020E4960D9836C6F843E1D3333FAE583C9CA26776F0D68DA69CE4A sent to the Savers vault. [https://gateway.liquify.com/chain/thorchain_api/thorchain/tx/status/A56B423250020E4960D9836C6F843E1D3333FAE583C9CA26776F0D68DA69CE4A](https://gateway.liquify.com/chain/thorchain_api/thorchain/tx/status/A56B423250020E4960D9836C6F843E1D3333FAE583C9CA26776F0D68DA69CE4A)

**Response**:

```json
{
  "tx": {
    "id": "A56B423250020E4960D9836C6F843E1D3333FAE583C9CA26776F0D68DA69CE4A",
    "chain": "BTC",
    "from_address": "bc1qmlw9x4xnkmyd5xgtvn5cuwc5jcq033g4cj2ur9",
    "to_address": "bc1q02hrv5y4dm7rux2swg020yzykhaufrglyv7kkj",
    "coins": [
      {
        "asset": "BTC.BTC",
        "amount": "30051812"
      }
    ],
    "gas": [
      {
        "asset": "BTC.BTC",
        "amount": "2500"
      }
    ],
    "memo": "+:BTC/BTC:t:0"
  },
  "stages": {
    "inbound_observed": {
      "completed": true
    },
    "inbound_finalised": {
      "completed": true
    }
  }
}
```
