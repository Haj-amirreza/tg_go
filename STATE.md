# وضعیت و ادامهٔ کار

## آخرین اصلاح: Coolify و یکپارچگی دامنه

دامنهٔ هدف اکنون https://bot.nymo.ir است. Labels کاربر پراکسی پورت 3000 داشت؛ تصویر روی 8080 گوش می‌دهد. درخواست‌های عمومی / و /healthz و /readyz خالی 404 بودند. Dockerfile ریشه با کش وابستگی و healthcheck بدون curl، aliasهای /panel و /admin با حفظ احراز هویت، و اصلاح قرارداد و نمایش production/botConnected اضافه شدند. بررسی‌ها: تست Go برای cmd/unibot و platform و config، تست Node پاسخ production، تولید دوبارهٔ قرارداد TypeScript و build پنل موفق شدند. تست Chromium روی API ساختگی production، نمایش وضعیت واقعی، شش مسیر مهمان، دو alias واردشده و RTL موبایل موفق بود. go vet و build باینری و اجرای healthcheck بدون DATABASE_URL/APP_KEY روی fixture آماده موفق بودند. Docker buildx --check بدون هشدار گذشت. Docker build کامل روی VPS اجرا نشده است. تنظیمات Coolify از این محیط تغییر نکرده و نصب زنده هنوز تأیید نشده؛ راهنما: docs/COOLIFY_FA.md.


نسخهٔ مستقل Go/PostgreSQL/Vue از checkpoint عمومی cbe185da91e35dfc7c217c63d01382d6f528c1c1 مخزن tg_bot استخراج شد؛ کد PHP، تنظیمات خصوصی و تاریخچهٔ Git کپی نشدند. importهای Go و نصاب به Haj-amirreza/tg_go و شاخهٔ main اشاره می‌کنند. ساختار مسیرها برای سازگاری Docker حفظ شده است.

نصب واقعی VPS هنوز تکمیل/تأیید نشده است. آخرین اجرای گزارش‌شدهٔ نصب قدیمی در مرحلهٔ Docker run متوقف شد؛ برای تشخیص، گزارش خصوصی /etc/unibot-web/last-operation-error.log لازم است. تغییر نشانی مخزن به‌تنهایی این خطا را رفع نمی‌کند. نصب موجود و تنظیماتش محفوظ بمانند؛ پیش از جابه‌جایی مخزن، خطا و مسیر ارتقا بررسی شوند.

قدم بعد: بررسی گزارش Docker، ادامهٔ نصب نسخهٔ قفل‌شده، تأیید HTTPS، ورود بانک‌ها، مدیران عددی و ارسال/دریافت واقعی Telegram. راهنما: docs/UBUNTU_INSTALL_FA.md.

## بررسی استخراج مستقل

- go test ./... و go vet ./... موفق؛ تست‌های نیازمند TEST_DATABASE_URL در این اجرا skip شدند.
- npm ci و npm run build پنل مستقل موفق.
- ۳۹ تست اسکریپت‌ها موفق (۹ production-ops، ۱۱ workflow، ۱۳ installer و ۶ web helper).
- نبود کد PHP و فایل تنظیمات خصوصی، عدم درج توکن/رمز داده‌شده و تطابق بایتی سه بانک اصلی بررسی شد.
- مالک مخزن خالی Haj-amirreza/tg_go را ساخت؛ نسخهٔ مستقل برای انتشار روی main آماده شد. سابقهٔ تهیه در شاخهٔ standalone/tg-go مخزن اصلی محفوظ است.
