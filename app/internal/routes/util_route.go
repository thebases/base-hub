package routes

// File overview:
// - Responsibility: implements Fiber transport handlers and request-bound orchestration for the util route surface.
// - Key symbols: `VietQrGen`, `AesHmacFunc`, `VietQRData`, `InputJsonObject`.
// - Pattern: Keeps transport concerns at the HTTP boundary: requests are parsed and validated here before business work is delegated deeper into services or the ORM runtime.

import (
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/base"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/utils"
	"git.thebase.vn/base_hub/internal/utils/vietqr"
	"github.com/gofiber/fiber/v3"
)

type VietQRData struct {
	Amount      string `json:"amount" validate:"required"`
	BillID      string `json:"bill_id" validate:"required"`
	BankAccount string `json:"bank_account" validate:"required"`
	BankBincode string `json:"bank_bincode" validate:"required"`
	Narrative   string `json:"narrative" `
}

func VietQrGen(c fiber.Ctx) error {
	logo := c.Query("logo", "")
	bankBincode := c.Query("binCode", "")
	bankAccount := c.Query("bankAccount", "")
	amount := c.Query("amount", "")
	narrative := c.Query("narrative", "CT")
	billId := c.Query("billId", "")

	// Public route: the bank account, amount and bill id are payment data, never logged (plan T36).

	// var vietQRData VietQRData
	// if err := c.Bind().Body(&vietQRData); err != nil {
	// 	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	// }

	// log.Infof("Validated Body: %s", vietQRData)

	vietQR := vietqr.NewVIETQR()
	vietQR.Fields.Acquirer = bankBincode
	vietQR.Fields.MerchantID = bankAccount
	vietQR.Fields.Amount = amount

	billLength := strconv.Itoa(len(billId))
	if len(billId) < 10 {
		billLength = "0" + billLength
	}

	vietQR.Fields.PurposeTxn = strings.TrimSpace(narrative + " BAS" + billLength + billId)

	// Assuming `logo` is a globally accessible variable or loaded earlier
	qrBase64 := vietQR.GenerateQRWithLogoBase64(logo)
	qrString := vietQR.ToString()
	return c.JSON(fiber.Map{
		"qrBase64": qrBase64,
		"qrString": qrString,
	})
}

// BankListFunc returns one bank by BIN code, or the whole directory when no code is given.
func BankListFunc(c fiber.Ctx) error {
	bankCode := c.Params("bankCode", "")
	// "%7BbankCode%7D" is the literal swagger placeholder; treat it as "no code".
	if bankCode != "" && bankCode != "%7BbankCode%7D" {
		bankData, exist := utils.GetBankInfo(bankCode)
		if !exist {
			return base.ServerError(c, fiber.StatusNotFound, "Bank not found")
		}
		return base.Success(c, fiber.Map{"bank": bankData})
	}
	return base.Success(c, utils.BankList())
}

type InputJsonObject struct {
	// JsonObj is a dynamic JSON object.
	// Example: {"id": 12345, "name": "John Doe", "email": "john.doe@example.com"}
	JsonObj       map[string]interface{} `json:"jsonObj" validate:"require"`
	KeyList       []string               `json:"keyList" validate:"require"  example:"['id', 'name']'"`
	NeedSignature bool                   `json:"needSign" validate:"require" default:"false"`
	NeedAES256    bool                   `json:"needAES256" validate:"require" default:"false"`
}

type InputJson struct {
	JsonObj map[string]interface{} `json:"jsonObj" validate:"require"`
	KeyList []string               `json:"keyList" validate:"require"  example:"['id','name']"`
}

func AesHmacFunc(c fiber.Ctx) error {
	// bankName := c.Params("bankName", "")
	var inputJsonObject InputJsonObject
	var err error
	if err := c.Bind().Body(&inputJsonObject); err != nil {
		return base.ServerError(c, fiber.StatusBadRequest, "invalid request body")
		// return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	// The payload is the data being signed/encrypted: never logged (plan T36).
	// result, err := BuildQueryStringFromJSON(inputJsonObject.JsonObj, inputJsonObject.KeyList)
	// if err != nil {
	// 	return base.ServerError(c, 500, "Fail to build query string")
	// }

	// log.Infof("Results: %s", result)
	hmacKey := ""
	rawData := ""
	signData := ""
	aesKey := ""
	minifiedJson := ""
	encryptData := ""
	// Marshal without indent (minified JSON)
	// minified, err := json.Marshal(inputJsonObject.JsonObj)
	// if err != nil {
	// 	panic(err)
	// }

	if inputJsonObject.NeedSignature {
		// key, err := utils.GenerateSecretHex(16) //0 mean usse default length
		// if err != nil {
		// 	return base.ServerError(c, 500, "Failed to generate secret key")
		// }
		// log.Infof("key 1: %s", key)
		// hmacKey = key
		// signData = utils.GenerateHMAC256(key, result)
		hmacKey, rawData, signData, err = utils.SignJSONHMAC("", inputJsonObject.JsonObj, inputJsonObject.KeyList)
		// Stop here: the AES branch below reuses err and would otherwise mask a signing failure.
		if err != nil {
			return base.ServerError(c, 500, err.Error())
		}

	}
	if inputJsonObject.NeedAES256 {
		// key, err := utils.GenerateSecretHex(16) //0 mean usse default length
		// if err != nil {
		// 	return base.ServerError(c, 500, "Failed to generate secret key")
		// }
		// log.Infof("key 1: %s", key)
		// aesKey = key
		// encryptData, err = utils.EncryptAES(key, string(minified))
		// if err != nil {
		// 	return base.ServerError(c, 500, "Failed to encrypt data")
		// }
		aesKey, minifiedJson, encryptData, err = utils.EncryptJSONAES("", inputJsonObject.JsonObj)

	}
	if err != nil {
		log.Errorf("Error: %s", err.Error())
		return base.ServerError(c, 500, err.Error())
	}
	return base.Success(c, fiber.Map{"hmacRawData": rawData, "hmacKey": hmacKey, "hmacSignData": signData, "aesMinifiedJson": minifiedJson, "aesKey": aesKey, "aesEncryptData": encryptData})

}

type AesObject struct {
	Data      string `json:"data" validate:"require"`
	Key       string `json:"key" validate:"require" `
	IsEncrypt bool   `json:"isEncrypt" validate:"require" default:"False"`
}

func AesFunc(c fiber.Ctx) error {
	var err error
	var aesObj AesObject
	if err := c.Bind().Body(&aesObj); err != nil {
		return base.ServerError(c, fiber.StatusBadRequest, "invalid request body")
		// return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	result := ""

	if aesObj.IsEncrypt {
		result, err = utils.EncryptAES(aesObj.Key, aesObj.Data)
		if err != nil {
			return base.ServerError(c, 500, err.Error())
		}
		return base.Success(c, fiber.Map{"encryptedData": result})
	} else {
		result, err = utils.DecryptAES(aesObj.Key, aesObj.Data)
		if err != nil {
			return base.ServerError(c, 500, err.Error())
		}
		return base.Success(c, fiber.Map{"rawData": result})
	}

}

func GenHexStringKeyFunc(c fiber.Ctx) error {
	hmacKey, err := utils.GenerateSecretHex(32)
	if err != nil {
		return base.ServerError(c, 500, "Fail to create keys")
	}
	aesKey, err := utils.GenerateSecretHex(32)
	if err != nil {
		return base.ServerError(c, 500, "Fail to create keys")
	}
	return base.Success(c, fiber.Map{"hmacKey": hmacKey, "aesKey": aesKey})

}

func JsonStringFunc(c fiber.Ctx) error {
	var inputJson InputJson
	if err := c.Bind().Body(&inputJson); err != nil {
		return base.ServerError(c, fiber.StatusBadRequest, "invalid request body")
		// return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	jsonString, err := utils.BuildQueryStringFromJSON(inputJson.JsonObj, inputJson.KeyList)
	if err != nil {
		return base.ServerError(c, fiber.StatusBadRequest, "invalid request body")
	}
	return base.Success(c, fiber.Map{"result": jsonString})

}
