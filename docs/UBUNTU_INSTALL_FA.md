# نصب tg_go روی Ubuntu

Ubuntu 22.04/24.04، معماری amd64/arm64، حداقل ۱۵ GiB فضای آزاد برای build. DNS دامنهٔ پنل باید به VPS اشاره کند؛ پورت‌های ورودی ۸۰/۴۴۳ باز باشند. برای گواهی اولیهٔ Cloudflare از DNS only استفاده کنید.

## نصب کامل

```bash
git clone https://github.com/Haj-amirreza/tg_go.git
cd tg_go
sudo bash tools/deploy/install-ubuntu.sh --domain panel.example.org --with-bot --owner-id YOUR_NUMERIC_TELEGRAM_ID --expected-bot YOUR_BOT_USERNAME
```

مقادیر نمونه را عوض کنید. برای مدیر مستقل دیگر `--admin-id NUMERIC_ID` را اضافه کنید. رمز پنل و توکن هنگام درخواست اسکریپت وارد می‌شوند؛ آن‌ها را در Git، چت یا خط فرمان نگذارید. نصب بدون `--with-bot` فقط پنل وب را راه می‌اندازد.

`--replace-proxy` سرویس قبلیِ صاحب پورت ۸۰/۴۴۳ را متوقف می‌کند؛ در شکست راه‌اندازی پراکسی جدید، قبلی را برمی‌گرداند. تنظیمات SSH/UFW خودکار تغییر نمی‌کنند.

اسکریپت SHA نسخه را قفل می‌کند و تنظیمات خصوصی `/etc/unibot-web/`، volumeهای PostgreSQL و فایل‌ها را حفظ می‌کند. برای تکرار نصب از همان SHA اعلام‌شده با `--ref FULL_COMMIT_SHA` استفاده کنید. config یا APP_KEY را پاک نکنید.

## مراحل جداگانه

از checkout تمیز همان نسخه اجرا کنید؛ نام مسیر بکاپ باید جدید باشد:

```bash
sudo python3 tools/deploy/install-data.py --backup-directory /var/backups/unibot-web/data-NEW_TIMESTAMP
sudo python3 tools/deploy/enable-telegram.py prepare --owner-id YOUR_NUMERIC_TELEGRAM_ID --expected-bot YOUR_BOT_USERNAME
sudo python3 tools/deploy/enable-telegram.py activate --backup-directory /var/backups/unibot-web/telegram-NEW_TIMESTAMP
sudo python3 tools/deploy/enable-telegram.py status
```

بانک همراه شامل ۷٬۹۹۱ کتاب و ۶۷۰ محتوای ارتباطی است؛ محتوای پیش‌نویس نیاز به تأیید مدیر دارد. ورود بانک همراه به معنی انتقال کاربران، تیکت‌ها یا آپلودهای نصب قدیمی نیست. فعال‌سازی Telegram پس از بررسی هویت ربات، اتصال مدیر و بکاپ انجام می‌شود و pending updateها حذف نمی‌شوند. قبل از تغییر webhook، ارسال‌کننده‌های نصب قبلی را متوقف کنید.

## گزارش خطا و توقف ارسال

```bash
sudo tail -n 60 /etc/unibot-web/last-operation-error.log
sudo python3 tools/deploy/enable-telegram.py disable
```

گزارش خطا خصوصی است؛ رمزهای تنظیم‌شده حذف می‌شوند. اگر عملیات متوقف شد، علت را بررسی کنید و همان نسخه را ادامه دهید؛ داده‌ها یا تنظیمات قبلی را حذف نکنید. پس از نصب، پنل در `https://YOUR_DOMAIN/login` است. دامنه، HTTPS و ارسال/دریافت واقعی Telegram باید روی VPS بررسی شوند.

مخزن Go نصب قدیمی را خودکار جابه‌جا نمی‌کند. مسیر releaseهای قبلی و بکاپ‌ها را نگه دارید. آزمون‌های مهاجرت آزمایشی فقط دیتابیس‌های `_test` را می‌پذیرند.
