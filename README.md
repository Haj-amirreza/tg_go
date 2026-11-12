# tg_go — همراه دانشگاه

ربات تلگرام با **Go + PostgreSQL** و پنل مدیریت **Vue 3 + TypeScript**؛ نسخهٔ مستقل بدون کد PHP.

- `backend/`: ربات، API، مدیریت، صف ارسال، مجوزها و migrationهای PostgreSQL.
- `frontend/admin/`: پنل فارسی راست‌چین با تم سفید و آبی.
- `database/seed/`: بانک کتاب‌ها، اطلاعات ارتباطی و محتوای پایهٔ همراه پروژه.
- `tools/deploy/`: نصب Ubuntu، بکاپ/ورود داده و فعال‌سازی Telegram.
- `contracts/openapi.yaml`: قرارداد API مشترک.

## نصب

[راهنمای نصب Ubuntu و اتصال Telegram](docs/UBUNTU_INSTALL_FA.md)

```bash
git clone https://github.com/Haj-amirreza/tg_go.git
cd tg_go
sudo bash tools/deploy/install-ubuntu.sh --domain panel.example.org --with-bot
```

قبل از اجرا، دامنه را به VPS متصل کنید و پورت‌های ۸۰/۴۴۳ را باز کنید. توکن و رمز فقط هنگام اجرا گرفته می‌شوند. اگر سرویس قبلی پورت وب را اشغال کرده است، گزینهٔ `--replace-proxy` برای جایگزینی کنترل‌شده وجود دارد.

## توسعه و بررسی

Go **1.27.4**، Node **24.22.1** و PostgreSQL **18**؛ وابستگی‌ها در فایل‌های lock ثابت‌اند.

```bash
cd backend
go test ./...
go vet ./...
```

آزمون‌های دیتابیس به `TEST_DATABASE_URL` با نام دیتابیس ختم‌شده به `_test` نیاز دارند؛ بدون آن اجرا نمی‌شوند. برای پنل، در `frontend/admin` دستورهای `npm ci` و `npm run build` را اجرا کنید. `tools/dev/` و `compose.next.yaml` برای توسعه موجودند.

منبع استخراج: [tg_bot، checkpoint cbe185d](https://github.com/Haj-amirreza/tg_bot/tree/cbe185da91e35dfc7c217c63d01382d6f528c1c1). نسخهٔ PHP و تاریخچهٔ قدیمی در همان مخزن محفوظ‌اند. داده‌ها و تاریخچهٔ خصوصی سرور در این مخزن نیستند. [وضعیت و ادامهٔ کار](STATE.md).
