import { createApp } from 'vue';
import { createPinia } from 'pinia';
import '@mdi/font/css/materialdesignicons.css';
import App from './App.vue';
import router from './router';
import vuetify from './plugins/vuetify';
import './styles/mobile.css';

const app = createApp(App);

// Pinia must be installed before the router: the navigation guard reads the
// auth store on the very first navigation.
app.use(createPinia());
app.use(router);
app.use(vuetify);

app.mount('#app');
