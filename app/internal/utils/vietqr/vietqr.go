package vietqr

// File overview:
// - Responsibility: collects shared helper logic for vietqr, giving the backend one reusable implementation for this low-level concern.
// - Key symbols: `NewVIETQR`, `Build`, `Fields`, `VIETQR`.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/draw"
	"image/png"
	"log"
	"math"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"
)

// Constants
const (
	ToAccount = "QRIBFTTA"
	NapasGUID = "A000000727"
)

// Currency mapping
var Currency = map[string]string{
	"VND": "704",
	"USD": "840",
}

// Fields struct
type Fields struct {
	IsDynamicQR       bool
	MerchantCategory  string
	MerchantName      string
	MerchantCity      string
	PostalCode        string
	Currency          string
	CountryCode       string
	Amount            string
	Acquirer          string
	MerchantID        string
	ServiceCode       string
	BillNumber        string
	MobileNumber      string
	StoreLabel        string
	LoyaltyNumber     string
	RefLabel          string
	CustomerLabel     string
	TerminalLabel     string
	PurposeTxn        string
	AdditionalData    string
	LangRef           string
	LocalMerchantName string
	LocalMerchantCity string
	UUID              string
	IPNURL            string
	AppPackageName    string
	CustomData        string
}

// Default field values
var DefaultFields = Fields{
	IsDynamicQR:       true,
	MerchantCategory:  "",
	MerchantID:        "",
	MerchantName:      "",
	MerchantCity:      "",
	PostalCode:        "",
	Currency:          "704",
	CountryCode:       "VN",
	Amount:            "0",
	Acquirer:          "970403",
	ServiceCode:       ToAccount,
	BillNumber:        "",
	MobileNumber:      "",
	StoreLabel:        "",
	LoyaltyNumber:     "",
	RefLabel:          "",
	CustomerLabel:     "",
	TerminalLabel:     "",
	PurposeTxn:        "",
	AdditionalData:    "",
	LangRef:           "",
	LocalMerchantName: "",
	LocalMerchantCity: "",
	UUID:              "",
	IPNURL:            "",
	AppPackageName:    "",
	CustomData:        "",
}

// VIETQR struct
type VIETQR struct {
	Data   []TLV
	Fields Fields
}

// Constructor
func NewVIETQR() *VIETQR {
	return &VIETQR{
		Fields: DefaultFields,
	}
}

// Build QR Data
func (v *VIETQR) Build() {
	v.Data = []TLV{
		NewTLV(0, "Payload Format Indicator", 2, true, "M", "01"),
		NewTLV(1, "QR Type", 2, true, "M", func() string {
			if v.Fields.IsDynamicQR {
				return "12"
			}
			return "11"
		}()),
		NewTLV(38, "QR code service on NAPAS system", 99, false, "M", []TLV{
			NewTLV(0, "Global Unique Identifier - GUID", 99, false, "M", NapasGUID),
			NewTLV(1, "Acquirer Information", 99, false, "M", []TLV{
				NewTLV(0, "Merchant Account Information", 6, false, "M", v.Fields.Acquirer),
				NewTLV(1, "Merchant ID", 19, true, "M", v.Fields.MerchantID),
			}),
			NewTLV(2, "Service Code", 10, true, "M", v.Fields.ServiceCode),
		}),
		NewTLV(52, "Merchant Category", 4, true, "M", v.Fields.MerchantCategory),
		NewTLV(53, "Transaction Currency", 3, true, "M", v.Fields.Currency),
	}

	if v.Fields.IsDynamicQR == true {
		v.Data = append(v.Data, NewTLV(54, "Transaction Amount", 13, true, "M", v.Fields.Amount))
	}
	v.Data = append(v.Data, NewTLV(58, "Country Code", 2, true, "M", v.Fields.CountryCode))
	v.Data = append(v.Data, NewTLV(59, "Merchant Name", 25, true, "M", v.Fields.MerchantName))
	v.Data = append(v.Data, NewTLV(60, "Merchant City", 15, true, "M", v.Fields.MerchantCity))
	v.Data = append(v.Data, NewTLV(62, "Additional Data Field", 99, false, "O", []TLV{
		NewTLV(1, "Bill Number", 25, false, "C", v.Fields.BillNumber),
		NewTLV(2, "Mobile Number", 25, false, "C", v.Fields.MobileNumber),
		NewTLV(3, "Store Label", 25, false, "C", v.Fields.StoreLabel),
		NewTLV(4, "Loyalty Number", 25, false, "C", v.Fields.LoyaltyNumber),
		NewTLV(5, "Reference Label", 25, false, "C", v.Fields.RefLabel),
		NewTLV(6, "Customer Label", 25, false, "C", v.Fields.CustomerLabel),
		NewTLV(7, "Terminal Label", 25, false, "C", v.Fields.TerminalLabel),
		NewTLV(8, "Purpose of Transaction", 25, false, "C", v.Fields.PurposeTxn),
		NewTLV(9, "Additional Data", 25, false, "C", v.Fields.AdditionalData),
		NewTLV(10, "Language Preference", 2, true, "C", v.Fields.LangRef),
		NewTLV(11, "Merchant Name - Alternate Language", 25, false, "C", v.Fields.LocalMerchantName),
		NewTLV(12, "Merchant City - Alternate Language", 25, false, "C", v.Fields.LocalMerchantCity),
		NewTLV(13, "UUID", 36, false, "M", v.Fields.UUID),
		NewTLV(14, "IPN URL", 25, false, "C", v.Fields.IPNURL),
		NewTLV(15, "App Package Name", 25, false, "C", v.Fields.AppPackageName),
		NewTLV(16, "Custom Data", 100, false, "C", v.Fields.CustomData),
	}))
}

// Convert QR Data to String
func (v *VIETQR) ToString() string {
	v.Build()
	builder := ""
	for _, tlv := range v.Data {
		builder += tlv.ToString()
	}
	semiQR := builder + "6304"
	crc := CRC{}.GetCRC16([]byte(semiQR))
	return semiQR + crc
}

func resizeImage(img image.Image, width int, height int) image.Image {
	rect := image.Rect(0, 0, width, height)
	resized := image.NewRGBA(rect)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			srcX := x * img.Bounds().Dx() / width
			srcY := y * img.Bounds().Dy() / height
			resized.Set(x, y, img.At(srcX, srcY))
		}
	}
	return resized
}

// Generate QR code with logo
func (v *VIETQR) GenerateQRWithLogoBase64(logoBase64 string) string {
	// var result image.Image
	// Generate QR code
	log.Println("QR Code:", v.ToString())
	qrCode, err := qr.Encode(v.ToString(), qr.H, qr.Auto)
	if err != nil {
		return "Encode error"
	}

	// Scale the QR code to the desired size
	qrCode, err = barcode.Scale(qrCode, 256, 256)
	if err != nil {
		return "Scale error"
	}

	qrImg := image.NewRGBA(qrCode.Bounds())
	draw.Draw(qrImg, qrCode.Bounds(), qrCode, image.Point{}, draw.Src)

	// Create a new image with the QR code and logo
	if logoBase64 != "" {

		// Decode the base64 image
		logoData, err := base64.StdEncoding.DecodeString(logoBase64)
		if err != nil {
			return "Base64 decode error"
		}

		// Decode the image
		logo, err := png.Decode(bytes.NewReader(logoData))
		if err != nil {
			return "Decode image error"
		}

		// Resize logo to fit in the center of the QR code
		logo = resizeImage(logo, 32, 32)

		// Create a circular mask for the logo
		logoBounds := logo.Bounds()
		mask := image.NewRGBA(logoBounds)
		radius := float64(logoBounds.Dx()) / 2
		center := image.Point{X: logoBounds.Dx() / 2, Y: logoBounds.Dy() / 2}

		for y := 0; y < logoBounds.Dy(); y++ {
			for x := 0; x < logoBounds.Dx(); x++ {
				dx := float64(x - center.X)
				dy := float64(y - center.Y)
				if math.Sqrt(dx*dx+dy*dy) <= radius {
					mask.Set(x, y, logo.At(x, y))
				} else {
					mask.Set(x, y, image.Transparent)
				}
			}
		}

		// Calculate the position to overlay the logo
		offset := image.Pt((qrImg.Bounds().Dx()-logo.Bounds().Dx())/2, (qrImg.Bounds().Dy()-logo.Bounds().Dy())/2)

		// Overlay the circular logo onto the QR code
		draw.Draw(qrImg, mask.Bounds().Add(offset), mask, image.Point{}, draw.Over)
	}

	// Encode the result to a buffer
	var buf bytes.Buffer
	if err := png.Encode(&buf, qrImg); err != nil {
		return "Encode final image error"
	}

	// Encode the buffer to base64
	base64Str := base64.StdEncoding.EncodeToString(buf.Bytes())
	return base64Str
}
