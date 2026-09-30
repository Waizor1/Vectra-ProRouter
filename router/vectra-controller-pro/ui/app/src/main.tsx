// Production entry: the IIFE bundle LuCI loads from /luci-static/vectra/vectra-app.js.
import { mount } from './mount';

declare const __APP_VERSION__: string;

export interface VectraAppApi {
  mount: typeof mount;
  version: string;
}

declare global {
  interface Window {
    VectraApp?: VectraAppApi;
  }
}

window.VectraApp = { mount, version: __APP_VERSION__ };
