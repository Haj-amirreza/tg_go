import { createApp } from "vue";
import { createPinia } from "pinia";
import { createRouter, createWebHistory } from "vue-router";
import App from "./App.vue";
import "./style.css";
const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: "/manage/support", component: () => import("./views/Support.vue") },
    {
      path: "/manage/student-life",
      component: () => import("./views/StudentLife.vue"),
    },
    { path: "/manage/leisure", component: () => import("./views/Leisure.vue") },
    { path: "/", component: () => import("./views/Dashboard.vue") },
    { path: "/manage/studio", component: () => import("./views/Studio.vue") },
    { path: "/manage/catalog", component: () => import("./views/Catalog.vue") },
    { path: "/manage", component: () => import("./views/Manage.vue") },
    { path: "/login", component: () => import("./views/Login.vue") },
  ],
});
createApp(App).use(createPinia()).use(router).mount("#app");
