// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import type { DeviceInfo } from "../shared/api";
import { Controller } from "./Controller";
import { calloutBox, controllerCallouts } from "./controller-callouts";
import { translator } from "./i18n";

afterEach(cleanup);

const info: DeviceInfo = {
  id: 1,
  name: "Xbox",
  type: "xboxone",
  vendor: 0,
  product: 0,
  version: 0,
  guid: "",
  path: "",
  driver: "",
  serial: "",
  gamepad: true,
  virtual: true,
};

it("measures a stick's leader from the box round both cells, and a button's from the button", () => {
  const { container } = render(<Controller info={info} t={translator("en")} />);
  // Every callout has an element to start its leader from.
  for (const callout of controllerCallouts(info))
    expect(
      calloutBox(container, callout.control),
      callout.control,
    ).not.toBeNull();
  const box = calloutBox(container, "leftstick");
  // The box holds both cells; asking for a cell's own id would give one cell.
  expect(box?.hasAttribute("data-callout-group")).toBe(true);
  expect(box?.querySelectorAll("button")).toHaveLength(2);
  expect(calloutBox(container, "rightstick")?.tagName).toBe("DIV");
  expect(calloutBox(container, "south")?.tagName).toBe("BUTTON");
});
