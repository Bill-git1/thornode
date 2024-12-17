package common

func NewAssetWithShortCodesV124(input string) (Asset, error) {
	shorts := make(map[string]string)

	shorts[ATOMAsset.ShortCode()] = ATOMAsset.String()
	shorts[AVAXAsset.ShortCode()] = AVAXAsset.String()
	shorts[BCHAsset.ShortCode()] = BCHAsset.String()
	shorts[BNBBEP20Asset.ShortCode()] = BNBBEP20Asset.String()
	shorts[BTCAsset.ShortCode()] = BTCAsset.String()
	shorts[DOGEAsset.ShortCode()] = DOGEAsset.String()
	shorts[ETHAsset.ShortCode()] = ETHAsset.String()
	shorts[LTCAsset.ShortCode()] = LTCAsset.String()
	shorts[RuneNative.ShortCode()] = RuneNative.String()

	long, ok := shorts[input]
	if ok {
		input = long
	}

	return NewAsset(input)
}
