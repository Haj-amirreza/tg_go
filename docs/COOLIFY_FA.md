# نصب روی Coolify v4.7.5 — bot.nymo.ir

## خطای فعلی و تنظیمات برنامه

بررسی عمومی دامنه در ۲۰۲۶/۱۱/۱۲ برای /، /healthz و /readyz پاسخ خالی 404 داد؛ /healthz برنامهٔ Go باید JSON بدهد. پراکسی ارسال‌شده از Coolify پورت 3000 را انتخاب کرده بود، ولی تصویر پروژه روی 8080 گوش می‌دهد. مسیر واقعی درخواست تا کانتینر و لاگ‌های سرور هنوز از این محیط بررسی نشده‌اند. استقرار دوبارهٔ کد به‌تنهایی تنظیم پورت ذخیره‌شده در Coolify را عوض نمی‌کند.

در تنظیمات Application:

| گزینه | مقدار |
|---|---|
| Repository / Branch | Haj-amirreza/tg_go / main |
| Build Pack | Dockerfile |
| Base Directory | / |
| Dockerfile Location | /Dockerfile |
| Ports Exposes | 8080 |
| Domains | https://bot.nymo.ir |

برای این برنامهٔ Dockerfile، دامنه را بدون پورت و بدون مسیر /panel یا /admin ثبت کنید. دامنه به کل سرویس Go متصل می‌شود؛ خود برنامه مسیرها را مدیریت می‌کند. Build/Start Command مربوط به Nixpacks را استفاده نکنید. پراکسی و TLS با Coolify هستند؛ Caddy مستقل compose.production.yaml را هم‌زمان روی پورت‌های میزبان بالا نیاورید.

تنظیمات را Save کنید و سپس Redeploy بزنید. اگر Labels را دستی قفل کرده‌اید، اجازه دهید Coolify آن‌ها را با پورت جدید بازتولید کند. مقصد loadbalancer.server.port باید 8080 باشد. قانون Host باید کوتیشن معتبر Traefik داشته باشد:

```text
Host(`bot.nymo.ir`) && PathPrefix(`/`)
```

Host(bot.nymo.ir) یا PathPrefix(/) بدون backtick قانون معتبر نیست. اگر backtick فقط موقع کپی متن حذف شده، نیازی به تغییر آن نیست. با افزودن /panel به Domains، روتینگ کل سایت را محدود نکنید.

## متغیرها و داده‌ها

کلیدها و اتصال دیتابیس نصب موجود را حفظ کنید. نمونهٔ نام تنظیمات:

```text
HTTP_ADDR=0.0.0.0:8080
STATIC_DIR=/web
APP_ENV=production
RELEASE_MODE=web-only
APP_ORIGIN=https://bot.nymo.ir
DB_POOL_MAX=6
PRIVATE_FILE_DIR=/files
```

DATABASE_URL باید به PostgreSQL قابل دسترس از کانتینر اشاره کند؛ localhost داخل کانتینر خود کانتینر است. APP_KEY همان کلید خصوصی ۶۴رقمی hex نصب باشد؛ با ساخت کلید جدید داده‌های رمز‌شدهٔ قبلی قابل خواندن نمی‌شوند. APP_ORIGIN باید دقیقاً دامنهٔ جدید و بدون / پایانی باشد تا ورود و CSRF درست کار کنند. برای /files یک فضای ذخیره‌سازی پایدار بسازید.

برای حالت bot از RELEASE_MODE=bot و تنظیمات کامل Telegram استفاده کنید؛ هویت ربات، مدیر عددی و فعال بودن صف هم باید قبلاً آماده شوند. این Dockerfile یک سرویس API/پنل را اجرا می‌کند؛ ارسال و پردازش Telegram به سرویس‌های worker، sender و scheduler همان نسخه نیاز دارد. صرف تنظیم دامنه ربات را فعال نمی‌کند. راهنمای مراحل داده و Telegram: [نصب](UBUNTU_INSTALL_FA.md). روی سروری که Coolify پراکسی را اداره می‌کند، installer مستقل با --replace-proxy را اجرا نکنید؛ آن گزینه برای جایگزین کردن پراکسی میزبان است.

Migration باید قبلاً با حساب owner روی همین دیتابیس اجرا شده باشد. API با کاربر محدود دیتابیس بالا بیاید؛ برای رفع خطای migration، مجوز superuser به API ندهید. حساب وب از admin-bootstrap ساخته می‌شود؛ آیدی Telegram جای رمز ورود وب نیست. فایل‌های env و رمزها را در Git یا لاگ اشتراکی نگذارید.

## مسیرهای یک دامنه

- https://bot.nymo.ir/ — صفحهٔ عمومی و وضعیت واقعی سرویس‌ها.
- https://bot.nymo.ir/panel و https://bot.nymo.ir/admin — پنل؛ بدون نشست معتبر به صفحهٔ ورود می‌روند.
- /panel/catalog، /panel/studio، /panel/support، /panel/student-life، /panel/leisure و معادل‌های /admin/ همان ابزارهای فعلی هستند.
- /login و /manage برای سازگاری حفظ شده‌اند؛ API زیر /api/v1/ و webhook زیر /api/v1/telegram/webhook است.

## سلامت و کندی build

پروب داخلی تصویر از /unibot -mode healthcheck استفاده می‌کند و /readyz را روی loopback می‌سنجد؛ scratch به curl، wget یا shell نیاز ندارد. اگر Coolify پروب سفارشی curl دارد، آن را با همین فرمان جایگزین کنید یا از HEALTHCHECK خود تصویر استفاده کنید. خطای دیتابیس یا migration باعث not ready می‌شود و باید از Logs رفع شود.

ساخت اول شامل دریافت imageها، وابستگی‌های Go/Node و کامپایل است؛ ۹ دقیقه به‌تنهایی زمان پاسخ برنامه را نشان نمی‌دهد. Dockerfile جدید نصب وابستگی‌ها و کامپایل را کش می‌کند؛ برای استفادهٔ مجدد، گزینهٔ build بدون کش را فعال نکنید. زمان build تازه هنوز روی VPS اندازه‌گیری نشده است.

پس از Redeploy:

```bash
curl -i https://bot.nymo.ir/healthz
curl -i https://bot.nymo.ir/readyz
```

انتظار: 200 و JSON؛ /readyz در خطای دیتابیس 503 است. اگر همچنان 404 خالی دارید، پورت، قانون Host و اتصال دامنه به همان Application را بررسی کنید. اگر 502/503 دارید، Runtime Logs و Healthcheck Logs را بخوانید. برای بررسی بعدی فقط لاگ خطاهای برنامه را با حذف اسرار بفرستید؛ نیازی به ارسال env یا رمز نیست.
