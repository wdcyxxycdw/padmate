// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it } from "vitest";
import type { ProfileBinding } from "../shared/generated/schema";
import { BindingFields } from "./BindingEditor";
import type { Language } from "./store";

afterEach(cleanup);

// The 连点 preset of the 连发 fields (MAP-23, ADR-0072): OUT-11's
// {style: "tap", delayMs: 0} at 12 a second. It only fills the values; they
// are edited like any other.

function Draft({
  initial,
  language = "zh-CN",
  control,
}: {
  initial: ProfileBinding;
  language?: Language;
  control?: string;
}) {
  const [value, setValue] = useState<ProfileBinding>(initial);
  return (
    <>
      <BindingFields
        control={control}
        value={value}
        onChange={setValue}
        language={language}
        actions={[]}
      />
      <output data-testid="draft">{JSON.stringify(value)}</output>
    </>
  );
}

const draft = (): ProfileBinding =>
  JSON.parse(screen.getByTestId("draft").textContent ?? "{}");

const turbo = () => screen.getByRole("button", { name: "连点" });
const field = (name: string) =>
  (screen.getByRole("textbox", { name }) as HTMLInputElement).value;

describe("the 连点 preset (MAP-23)", () => {
  it("fills no wait and 12 a second, as taps, over what the repeat was", () => {
    render(
      <Draft
        initial={{
          press: {
            type: "keys",
            keys: "F13",
            repeat: { style: "typematic", delayMs: 300, rateHz: 40 },
          },
        }}
      />,
    );
    expect(field("开始前等待（ms）")).toBe("300");
    fireEvent.click(turbo());
    // Without a mode, a tap repeat style is a tap, as the daemon reads it.
    expect(draft().press).toEqual({
      type: "keys",
      keys: "F13",
      repeat: { style: "tap", delayMs: 0, rateHz: 12 },
    });
    // The fields show what was filled, and the way of sending follows.
    expect(field("开始前等待（ms）")).toBe("0");
    expect(field("每秒次数")).toBe("12");
    expect(
      screen
        .getByRole("radio", { name: "单次点按" })
        .getAttribute("aria-checked"),
    ).toBe("true");
  });

  it("changes a mode that was chosen to tap, which is what a turbo is", () => {
    render(
      <Draft
        initial={{
          press: {
            type: "keys",
            keys: "F13",
            mode: "hold",
            repeat: { style: "typematic", delayMs: 350, rateHz: 12 },
          },
        }}
      />,
    );
    fireEvent.click(turbo());
    expect(draft().press).toEqual({
      type: "keys",
      keys: "F13",
      mode: "tap",
      repeat: { style: "tap", delayMs: 0, rateHz: 12 },
    });
  });

  it("does the same for the clicks of a mouse button", () => {
    render(
      <Draft
        initial={{
          press: {
            type: "keys",
            keys: "MouseLeft",
            mode: "tap",
            repeat: { style: "tap", delayMs: 350, rateHz: 12 },
          },
        }}
      />,
    );
    fireEvent.click(turbo());
    expect(draft().press).toEqual({
      type: "keys",
      keys: "MouseLeft",
      mode: "tap",
      repeat: { style: "tap", delayMs: 0, rateHz: 12 },
    });
  });

  it("is one edit, and the values can be changed after it", () => {
    render(
      <Draft
        initial={{
          press: {
            type: "keys",
            keys: "F13",
            repeat: { style: "tap", delayMs: 350, rateHz: 12 },
          },
        }}
      />,
    );
    fireEvent.click(turbo());
    const rate = screen.getByRole("textbox", { name: "每秒次数" });
    fireEvent.change(rate, { target: { value: "30" } });
    fireEvent.blur(rate);
    expect(draft().press).toEqual({
      type: "keys",
      keys: "F13",
      repeat: { style: "tap", delayMs: 0, rateHz: 30 },
    });
  });

  it("is there only while the key repeats", () => {
    render(<Draft initial={{ press: { type: "keys", keys: "F13" } }} />);
    expect(screen.queryByRole("button", { name: "连点" })).toBeNull();
    fireEvent.click(screen.getByRole("switch", { name: "连发" }));
    expect(turbo()).toBeTruthy();
  });

  it("is left out where the repeat follows how far a stick is pushed", () => {
    render(
      <Draft
        control="lefttrigger"
        initial={{
          press: {
            type: "keys",
            keys: "F13",
            repeat: { style: "tap", delayMs: 350, rateHz: 20, minRateHz: 4 },
          },
        }}
      />,
    );
    expect(screen.queryByRole("button", { name: "连点" })).toBeNull();
  });

  it("speaks English", () => {
    render(
      <Draft
        language="en"
        initial={{
          press: {
            type: "keys",
            keys: "F13",
            repeat: { style: "tap", delayMs: 350, rateHz: 20 },
          },
        }}
      />,
    );
    const button = screen.getByRole("button", { name: "Turbo" });
    expect(button.getAttribute("title")).toBe("Starts at once, 12 per second");
    fireEvent.click(button);
    expect(draft().press).toMatchObject({
      repeat: { style: "tap", delayMs: 0, rateHz: 12 },
    });
  });
});
