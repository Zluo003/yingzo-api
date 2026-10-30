import React from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App.jsx";
import { LocaleProvider } from './i18n/LocaleProvider.jsx'
import "./styles.css";

createRoot(document.getElementById("root")).render(
  <React.StrictMode>
    <LocaleProvider component={App}/>
  </React.StrictMode>,
);
