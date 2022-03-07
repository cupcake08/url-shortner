package routes

import (
	"os"
	"strconv"
	"time"

	"github.com/asaskevich/govalidator"
	"github.com/cupcake08/url-shortner/database"
	helpers "github.com/cupcake08/url-shortner/helper"
	"github.com/go-redis/redis/v8"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type request struct {
	URL             string        `json:"url"`
	CustomShortName string        `json:"short"`
	Expiry          time.Duration `json:"expiry"`
}

type response struct {
	URL             string        `json:"url"`
	CustomShortName string        `json:"short"`
	Expiry          time.Duration `json:"expiry"`
	XRateRemaining  int           `json:"rate_limit"`
	XRateLimitReset time.Duration `json:"rate_limit_reset"`
}

func ShortenURL(c *fiber.Ctx) error {
	body := new(request)
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "cannot parse JSON"})
	}
	//implement rate limiting
	r2 := database.CreateClient(0)
	defer r2.Close()

	val, err := r2.Get(database.Ctx, c.IP()).Result()

	if err == redis.Nil {
		_ = r2.Set(database.Ctx, c.IP(), os.Getenv("API_QUOTA"), time.Second*30*60).Err()
	} else {
		valInt, _ := strconv.Atoi(val)
		if valInt <= 0 {
			limit, _ := r2.TTL(database.Ctx, c.IP()).Result()
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"error":            "Rate limit exceeded",
				"rate_limit_reset": limit / time.Nanosecond / time.Minute,
			})
		}
	}
	//check if input is an actual url
	if !govalidator.IsURL(body.URL) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid url"})
	}

	//check for domain error
	if !helpers.RemoveDomainError(body.URL) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid domain"})
	}

	//enforce ssl certificate
	body.URL = helpers.EnforceHTTP(body.URL)

	var id string

	if body.CustomShortName == "" {
		id = uuid.New().String()[:6]
	} else {
		id = body.CustomShortName
	}

	r := database.CreateClient(1)
	defer r.Close()

	//check if somebody is already using that custom URL
	val, _ = r.Get(database.Ctx, id).Result()
	if val != "" {
		//something is found in database
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "URL custom short is already in use"})

	}

	//checking the Expiry
	if body.Expiry == 0 {
		body.Expiry = 24
	}

	err = r.Set(database.Ctx, id, body.URL, body.Expiry*3600*time.Second).Err()

	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "unable to connect to server"})
	}

	res := response{
		URL:             body.URL,
		CustomShortName: "",
		Expiry:          body.Expiry,
		XRateLimitReset: 30,
		XRateRemaining:  10,
	}

	r2.Decr(database.Ctx, c.IP())

	val, _ = r2.Get(database.Ctx, c.IP()).Result()
	res.XRateRemaining, _ = strconv.Atoi(val)

	ttl, _ := r2.TTL(database.Ctx, c.IP()).Result()
	res.XRateLimitReset = ttl / time.Nanosecond / time.Minute

	res.CustomShortName = os.Getenv("DOMAIN") + "/" + id
	return c.Status(fiber.StatusOK).JSON(res)
}
