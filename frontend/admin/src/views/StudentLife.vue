<script setup lang="ts">
import { ref, reactive, onMounted, computed, toRaw } from "vue";
import { useRoute } from "vue-router";
import { useAuth } from "../stores/auth";
import { domainRequest as api, commandKey } from "../lib/domain-api";
type Ad = {
  id: string;
  userId: number | null;
  kind: string;
  title: string;
  body: string;
  contact: string;
  url: string;
  place: string;
  price: number | null;
  state: string;
  note: string;
  revision: number;
  expiresAt: number;
  consent: boolean;
};
type Verification = {
  userId: string;
  status: string;
  revision: number;
  note: string;
  details: Record<string, string>;
};
const auth = useAuth(),
  route = useRoute(),
  kind = ref(String(route.query.kind || "channels")),
  page = ref(0),
  filter = ref("pending"),
  items = ref<Ad[]>([]),
  selected = ref<string[]>([]),
  verifications = ref<Verification[]>([]),
  error = ref(""),
  notice = ref(""),
  busy = ref(false),
  editing = ref(false),
  review = ref<Verification | null>(null),
  checked = ref(false),
  second = ref(false),
  note = ref(""),
  reviewState = ref("approved");
const draft = reactive<Ad>({
  id: "0",
  userId: null,
  kind: "channels",
  title: "",
  body: "",
  contact: "",
  url: "",
  place: "",
  price: null,
  state: "pending",
  note: "",
  revision: 0,
  expiresAt: 0,
  consent: false,
});
const allowed = computed(() =>
  kind.value === "verifications"
    ? auth.can("users")
    : kind.value === "channels"
      ? auth.can("content")
      : auth.can("market"),
);
let signature = "",
  key = "";
function keyFor(v: unknown) {
  const s = JSON.stringify(v);
  if (signature !== s) {
    signature = s;
    key = commandKey();
  }
  return key;
}
async function run(fn: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  error.value = "";
  try {
    await fn();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function command<T>(path: string, payload: unknown) {
  return api<T>(
    path,
    "POST",
    payload,
    auth.actor?.csrf,
    keyFor({ path, payload }),
  );
}
async function load() {
  selected.value = [];
  if (!allowed.value) return;
  if (kind.value === "verifications")
    verifications.value = await api("verifications?page=" + page.value);
  else
    items.value = await api(
      `community?kind=${kind.value}&state=${filter.value}&page=${page.value}`,
    );
}
function fresh() {
  Object.assign(draft, {
    id: "0",
    userId: null,
    kind: kind.value,
    title: "",
    body: "",
    contact: "",
    url: "",
    place: "",
    price: null,
    state: "pending",
    note: "",
    revision: 0,
    expiresAt: Math.floor(Date.now() / 1000) + 7 * 86400,
    consent: false,
  });
  editing.value = true;
  signature = "";
}
function edit(a: Ad) {
  Object.assign(draft, structuredClone(toRaw(a)));
  editing.value = true;
  signature = "";
}
async function save() {
  await run(async () => {
    Object.assign(draft, await command<Ad>("community/save", toRaw(draft)));
    notice.value = "تغییرات ذخیره شد.";
    await load();
  });
}
async function archive() {
  if (
    !selected.value.length ||
    !confirm("موارد انتخاب‌شده حذف شوند؟ راه تماس آگهی‌ها هم پاک می‌شود.")
  )
    return;
  await run(async () => {
    await command("community/archive", {
      kind: kind.value,
      items: items.value
        .filter((a) => selected.value.includes(a.id))
        .map((a) => ({ id: a.id, revision: a.revision })),
    });
    notice.value = "موارد انتخاب‌شده بایگانی شدند.";
    editing.value = false;
    await load();
  });
}
async function saveReview() {
  if (!review.value) return;
  await run(async () => {
    await command("verifications/review", {
      userId: review.value!.userId,
      revision: review.value!.revision,
      status: reviewState.value,
      note: note.value,
      checked: checked.value,
      secondAccount: second.value,
    });
    review.value = null;
    notice.value = "نتیجهٔ بررسی ذخیره شد.";
    await load();
  });
}
onMounted(async () => {
  await auth.load();
  if (!allowed.value) {
    kind.value = auth.can("content")
      ? "channels"
      : auth.can("market")
        ? "market"
        : "verifications";
  }
  await run(load);
});
</script>
<template>
  <div class="catalog-page">
    <header class="page-heading">
      <div>
        <p class="eyebrow">جامعهٔ دانشجویی، با مدیریت روشن</p>
        <h1>امکانات دانشجویی</h1>
        <p class="muted">ثبت، بررسی و مدیریت کانال‌ها و آگهی‌های دانشجویان.</p>
      </div>
      <RouterLink class="button secondary" to="/manage">حساب مدیریت</RouterLink>
    </header>
    <p v-if="error" class="error-box" role="alert">{{ error }}</p>
    <p v-if="notice" class="notice-box" role="status">{{ notice }}</p>
    <div class="catalog-toolbar">
      <select
        v-model="kind"
        aria-label="بخش دانشجویی"
        :disabled="busy"
        @change="
          page = 0;
          editing = false;
          review = null;
          run(load);
        "
      >
        <option v-if="auth.can('content')" value="channels">
          کانال‌های دانشجویان
        </option>
        <option v-if="auth.can('market')" value="market">بازارچه</option>
        <option v-if="auth.can('market')" value="lost">
          گمشده‌ها و پیداشده‌ها
        </option>
        <option v-if="auth.can('users')" value="verifications">
          احراز دانشجویی
        </option></select
      ><select
        v-if="kind !== 'verifications'"
        v-model="filter"
        aria-label="فیلتر وضعیت"
        :disabled="busy"
        @change="
          page = 0;
          run(load);
        "
      >
        <option value="">همهٔ وضعیت‌ها</option>
        <option value="pending">در انتظار بررسی</option>
        <option v-if="kind === 'channels'" value="approved">تأییدشده</option>
        <option v-else value="published">منتشرشده</option>
        <option value="rejected">ردشده</option>
        <option value="deleted">حذف‌شده</option></select
      ><button
        v-if="kind !== 'verifications' && allowed"
        class="button"
        :disabled="busy"
        @click="fresh"
      >
        افزودن مورد</button
      ><button
        v-if="selected.length"
        class="button danger"
        :disabled="busy"
        @click="archive"
      >
        حذف انتخاب‌شده‌ها
      </button>
    </div>
    <p v-if="!allowed" role="alert">اجازهٔ مدیریت این بخش را ندارید.</p>
    <template v-else
      ><section class="panel catalog-list">
        <template v-if="kind === 'verifications'"
          ><article
            v-for="v in verifications.slice(0, 10)"
            :key="v.userId"
            class="catalog-row"
          >
            <div>
              <strong>{{ v.details.full_name }} · #{{ v.userId }}</strong
              ><small>{{ v.status }} · نسخه {{ v.revision }}</small>
            </div>
            <button
              class="button secondary"
              :disabled="busy"
              @click="
                review = v;
                checked = false;
                second = false;
                note = v.note;
                reviewState = v.status === 'approved' ? 'revoked' : 'approved';
              "
            >
              بررسی درخواست
            </button>
          </article></template
        ><template v-else
          ><article
            v-for="a in items.slice(0, 10)"
            :key="a.id"
            class="catalog-row"
          >
            <input
              v-model="selected"
              type="checkbox"
              :value="a.id"
              :aria-label="'انتخاب ' + a.title"
            />
            <div>
              <strong>{{ a.title }}</strong
              ><small>{{ a.state }} · نسخه {{ a.revision }}</small>
            </div>
            <button class="button secondary" :disabled="busy" @click="edit(a)">
              بررسی و ویرایش
            </button>
          </article></template
        >
        <p
          v-if="
            kind === 'verifications' ? !verifications.length : !items.length
          "
        >
          موردی در این صفحه نیست.
        </p>
        <div class="catalog-pagination">
          <button
            class="button secondary"
            :disabled="busy || page === 0"
            @click="
              page--;
              run(load);
            "
          >
            صفحه قبل</button
          ><span>صفحه {{ page + 1 }}</span
          ><button
            class="button secondary"
            :disabled="
              busy ||
              (kind === 'verifications' ? verifications.length : items.length) <
                11
            "
            @click="
              page++;
              run(load);
            "
          >
            صفحه بعد
          </button>
        </div>
      </section>
      <div v-if="editing" class="studio-layout">
        <form class="panel studio-editor" @submit.prevent="save">
          <div class="section-heading">
            <h2>ثبت و بررسی محتوا</h2>
            <button
              type="button"
              class="button secondary"
              @click="editing = false"
            >
              بستن فرم
            </button>
          </div>
          <label
            >عنوان<input
              v-model="draft.title"
              maxlength="160"
              required /></label
          ><label
            >متن و توضیح<textarea
              v-model="draft.body"
              rows="5"
              :maxlength="kind === 'channels' ? 500 : 5000"
              required
            ></textarea></label
          ><label v-if="draft.kind === 'channels'"
            >لینک یا آیدی کانال<input
              v-model="draft.url"
              dir="ltr"
              required
              maxlength="255" /></label
          ><template v-else
            ><label
              >شناسهٔ کاربر مالک<input
                v-model.number="draft.userId"
                type="number"
                min="1"
                required
                :disabled="draft.id !== '0'" /></label
            ><label v-if="draft.kind === 'market'"
              >قیمت به تومان<input
                v-model.number="draft.price"
                type="number"
                min="0"
                max="1000000000000" /></label
            ><label v-if="['lost', 'found'].includes(draft.kind)"
              >نوع آگهی<select v-model="draft.kind" aria-label="نوع آگهی">
                <option value="lost">گمشده</option>
                <option value="found">پیداشده</option>
              </select></label
            ><label v-if="draft.kind !== 'market'"
              >محل<input v-model="draft.place" maxlength="200" /></label
            ><label
              >راه تماس<input v-model="draft.contact" maxlength="300" /></label
            ><label
              ><input v-model="draft.consent" type="checkbox" /> اجازهٔ انتشار
              راه تماس بررسی شده است</label
            ></template
          ><label
            >وضعیت بررسی<select v-model="draft.state" aria-label="وضعیت بررسی">
              <option value="pending">در انتظار بررسی</option>
              <option v-if="draft.kind === 'channels'" value="approved">
                تأیید و انتشار
              </option>
              <option v-else value="published">تأیید و انتشار</option>
              <option value="rejected">ردشده</option>
              <option value="deleted">حذف‌شده</option>
            </select></label
          ><label
            >یادداشت بررسی<textarea
              v-model="draft.note"
              maxlength="1000"
              rows="2"
            ></textarea></label
          ><button class="button" :disabled="busy">ذخیرهٔ محتوا</button>
        </form>
        <section
          class="telegram-preview"
          aria-label="پیش‌نمایش محتوای دانشجویی"
        >
          <div class="preview-label">پیش‌نمایش · هنوز ثبت نشده</div>
          <article class="telegram-bubble">
            <h3>{{ draft.title }}</h3>
            <p class="pre-wrap">{{ draft.body }}</p>
            <p>{{ draft.url }}</p>
            <p>{{ draft.contact }}</p>
            <p>{{ draft.place }}</p>
            <small v-if="draft.price !== null">{{ draft.price }} تومان</small>
          </article>
        </section>
      </div>
      <section v-if="review" class="panel studio-editor">
        <h2>بررسی احراز دانشجویی #{{ review.userId }}</h2>
        <dl>
          <template v-for="(value, key) in review.details" :key="key"
            ><dt>
              {{
                key === "full_name"
                  ? "نام کامل"
                  : key === "student_number"
                    ? "شماره دانشجویی"
                    : key === "phone"
                      ? "شماره تماس"
                      : key
              }}
            </dt>
            <dd dir="auto">{{ value }}</dd></template
          >
        </dl>
        <label
          >نتیجهٔ بررسی<select v-model="reviewState" aria-label="نتیجهٔ بررسی">
            <option v-if="review.status === 'pending'" value="approved">
              تأیید دانشجویی
            </option>
            <option v-if="review.status === 'pending'" value="rejected">
              رد درخواست
            </option>
            <option v-if="review.status === 'approved'" value="revoked">
              لغو تأیید
            </option>
          </select></label
        ><label v-if="reviewState === 'approved'"
          ><input v-model="checked" type="checkbox" /> دانشجوبودن را در مرجع
          دانشگاه بررسی کردم</label
        ><label v-if="reviewState === 'approved'"
          ><input v-model="second" type="checkbox" /> حساب دوم با بررسی مستقل
          تأیید شود</label
        ><label
          >یادداشت بررسی<textarea
            v-model="note"
            rows="2"
            maxlength="1000"
          ></textarea></label
        ><button
          class="button"
          :disabled="busy || (reviewState === 'approved' && !checked)"
          @click="saveReview"
        >
          ثبت نتیجهٔ بررسی</button
        ><button class="button secondary" @click="review = null">بستن</button>
      </section></template
    >
  </div>
</template>
