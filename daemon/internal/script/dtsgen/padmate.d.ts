declare module "padmate" {
  /** @since 0.5 @macro */
  export const host: {
    readonly version: string; readonly api: string; readonly mode: "run" | "describe" | "test";
    has(feature: Feature): boolean;
  };
  /** @since 0.5 */
  export type Capability = "clipboard" | "exec";
  /** @since 0.5 */
  export type Feature = "hud" | "rumble" | "led" | "gyro" | "touchpad" | "mouse" | "clipboard" | "exec";

  // ---- 定义 ----
  /** @since 0.5 @until 0.6 */ export function defineScript(def: ScriptDef): ScriptDef;
  /** @since 0.6 */ export function defineScript<P extends ParamSchema>(def: ScriptDef<P>): ScriptDef<P>;
  /** @since 0.6 */ export function defineAction<P extends ParamSchema, const A extends ParamSchema>(
      def: ActionDef<P, A>): ActionDef<P, A>;
  /** @since 0.5 @until 0.6 */
  export interface ScriptDef {
    setup?(): void;
    onActivate?(): void | Promise<void>;
    teardown?(): void;
  }
  /** @since 0.6 */
  export interface ScriptDef<P extends ParamSchema = {}> {
    params?: P;
    actions?: Record<string, ActionDef<NoInfer<P>, any>>;
    setup?(ctx: ScriptContext<P>): void;
    onActivate?(ctx: ScriptContext<P>): void | Promise<void>;
    teardown?(): void;
  }
  /** @since 0.6 */
  export interface ScriptContext<P extends ParamSchema> { readonly params: ParamValues<P>; readonly profile: string; }
  /** @since 0.6 */
  export interface ActionDef<P extends ParamSchema, A extends ParamSchema> {
    label?: string; args?: A;
    run(args: ParamValues<A>, ctx: ScriptContext<P>, e: InputEvent): void | Promise<void>;
  }

  /** @since 0.6 */
  export const param: {
    number(o: { label?: string; default: number; min?: number; max?: number; step?: number }): ParamSpec<number>;
    boolean(o: { label?: string; default: boolean }): ParamSpec<boolean>;
    choice<const T extends string>(o: { label?: string; options: readonly T[]; default: T }): ParamSpec<T>;
    text(o: { label?: string; default: string; maxLength?: number }): ParamSpec<string>;
    key(o: { label?: string; default: KeySpec }): ParamSpec<KeySpec>;
    control(o: { label?: string; default: Control }): ParamSpec<Control>;
    layer(o: { label?: string; default: string }): ParamSpec<string>;
  };
  /** @since 0.6 */
  export interface ParamSpec<T> { readonly __t?: T }
  /** @since 0.6 */
  export type ParamSchema = Record<string, ParamSpec<any>>;
  /** @since 0.6 */
  export type ParamValues<S extends ParamSchema> = { readonly [K in keyof S]: S[K] extends ParamSpec<infer T> ? T : never };

  // ---- 控件 ----
  /** @since 0.5 */
  export type Button = "south" | "east" | "west" | "north" | "back" | "guide" | "start"
    | "leftstick" | "rightstick" | "leftshoulder" | "rightshoulder"
    | "dpad.up" | "dpad.down" | "dpad.left" | "dpad.right"
    | `misc${1 | 2 | 3 | 4 | 5 | 6}` | `paddle${1 | 2 | 3 | 4}` | "touchpad";
  /** @since 0.5 */
  export type ButtonAlias = "a" | "b" | "x" | "y" | "lb" | "rb" | "l3" | "r3" | "select";
  /** @since 0.5 */
  export type Dir8 = "up" | "down" | "left" | "right" | "upleft" | "upright" | "downleft" | "downright";
  /** @since 0.5 */
  export type StickName = "ls" | "rs";
  /** @since 0.5 */
  export type TriggerName = "lt" | "rt";
  /** @since 0.5 */
  export type VirtualControl = TriggerName | `${TriggerName}.full` | `${StickName}.${Dir8 | "active" | "inner" | "outer" | "cw" | "ccw"}` | `dpad.${Dir8}`;
  /** @since 0.5 */
  export type Control = Button | ButtonAlias | VirtualControl;
  /** @since 0.5 */
  export type Axis = "leftx" | "lefty" | "rightx" | "righty" | TriggerName;
  /** @since 0.6 */
  export type ClaimTarget = Control | StickName | "dpad";

  // ---- 手柄 ----
  /** @macro */
  export const pad: {
    /** @since 0.5 */ readonly info: DeviceInfo;
    /** @since 0.5 */ has(c: Control | Axis): boolean;
    /** @since 0.10 */ label(c: Control): string;          // 按设备族返回 "A"/"×" 等
    /** @since 0.5 */ isDown(c: Control): boolean;
    /** @since 0.5 */ heldFor(c: Control): number;        // ms，未按下时为 0
    /** @since 0.5 */ axis(a: Axis): number;              // 原始值 -1..1（扳机 0..1），+y 向上
    /** @since 0.6 */ stick(s: StickName, o?: StickOptions): StickState; // 默认使用 profile 的 analog 配置
    /** @since 0.6 */ trigger(t: TriggerName): number;
    /** @since 1.3 */ gyro(): { x: number; y: number; z: number } | undefined;
    /** @since 1.3 */ accel(): { x: number; y: number; z: number } | undefined;
    /** @since 1.3 */ touchpad(finger?: number): { x: number; y: number; down: boolean } | undefined;
    /** @since 0.17 */ rumble(low: number, high: number, ms: number): void;           // 强度 0..1，ms 取整后限制在 1..5000，0 停止
    /** @since 0.17 */ rumbleTriggers(left: number, right: number, ms: number): void;  // 同上，只有扳机带马达的手柄才有反应
    /** @since 0.17 */ haptic(preset: "tick" | "confirm" | "error" | `pulse${1 | 2 | 3 | 4}`): void;  // 与绑定的 haptic 是同一组预设
    /** @since 0.17 */ setLed(rgb: string | null): void;  // "#RRGGBB"；null 把灯还给层的颜色
  };
  /** @since 0.5 */
  export interface DeviceInfo {
    readonly key: string; readonly name: string;
    readonly type: "xbox" | "ps4" | "ps5" | "switchPro" | "joyconLeft" | "joyconRight" | "joyconPair" | "other";
    /** @since 0.17 */ readonly battery?: { state: "wired" | "charging" | "discharging" | "unknown"; percent?: number };  // 读到过电量才有；不报告百分比时没有 percent
  }
  /** @since 0.6 */
  export interface StickOptions { deadzone?: number; outer?: number; curve?: "linear" | "quadratic" | "cubic" }
  /** @since 0.6 */
  export interface StickState {
    readonly x: number; readonly y: number; readonly magnitude: number; readonly angle: number; // 0°=上，顺时针
    direction(sectors: 4 | 8, opts?: { offsetDeg?: number; hysteresisDeg?: number; previous?: number }): number; // -1 表示死区内
  }

  // ---- 输入 ----
  /** @since 0.5 */
  export interface GestureMap {
    press: true; release: true;
    /** @since 0.6 */ tap: true;
    /** @since 0.6 */ doubleTap: true;
    /** @since 0.6 */ tripleTap: true;
    /** @since 0.6 */ quadTap: true;
    /** @since 0.6 */ hold: true;
  }
  export type Gesture = keyof GestureMap;
  /** @since 0.6 */
  export interface GestureOptions { holdMs?: number; multiTapMs?: number; priority?: number; label?: string }
  /** @since 0.5 */
  export interface InputEvent {
    readonly control: Control; readonly gesture: Gesture;
    readonly time: number; readonly duration: number; readonly count: number;
    readonly scope: "global" | "base" | "common" | `layer:${string}`;
    readonly cycle: number;
    /** @since 0.6 */ readonly held: boolean;
    pass(): void;
    /** @since 0.6 */
    /** 只在 press、hold 或按住期间的 press/hold 绑定动作中有效；否则抛出
     *  TypeError("holdKeys is only valid while the control is held; use key.tap") */
    holdKeys(spec: KeySpec): void;
    /** @since 0.6 */
    holdLayer(name: string): void;
    /** @since 0.6 */
    onEnd(fn: () => void): void;                 // 按压已结束时作为微任务执行
  }
  /** @since 0.5 */
  export type Handler = (e: InputEvent) => void | Promise<void>;
  /** @since 0.5 */
  export interface Scope {
    on(c: Control, g: Gesture, h: Handler): Disposable;
    /** @since 0.6 */ on(c: Control, g: Gesture, h: Handler, o?: GestureOptions): Disposable;
    /** @since 0.6 */ claim(t: ClaimTarget): Disposable;
    /** @since 0.23 */ bind(c: Control, binding: BindingJson): Disposable;
  }
  /** @since 0.5 */
  export interface Disposable { dispose(): void }
  /** @since 0.23 */
  export type BindingJson = Record<string, unknown>; // 与 profile.json 的 Binding 相同（由 schema 生成）
  /** base 层脚本级 @since 0.5 */ export const on: Scope["on"];
  /** @since 0.6 */ export const claim: Scope["claim"];
  /** @since 0.5 */ export function global(): Scope;
  /** @since 0.6 */ export function layer(name: string): Scope;
  /** 只观察、不消费 @since 0.5 */ export function observe(c: Control, h: (e: InputEvent) => void): Disposable;
  /** @since 0.6 */ export function onAxis(a: Axis, h: (v: number, dt: number) => void): Disposable;
  /** @since 0.6 */ export function onStick(s: StickName, h: (st: StickState, dt: number) => void): Disposable;
  /** @since 0.6 */ export function onTick(h: (dt: number) => void): Disposable;

  /** @since 0.6 */
  export const layers: {
    readonly base: string; readonly active: readonly string[];
    switch(name: string): void; enable(name: string): void; disable(name: string): void; toggle(name: string): void;
    hold(name: string): Disposable; isActive(name: string): boolean;
    onChange(h: (active: readonly string[]) => void): Disposable;
  };

  // ---- 输出 ----
  /** @since 0.5 */
  export type Modifier = "Ctrl" | "LCtrl" | "RCtrl" | "Shift" | "LShift" | "RShift" | "Alt" | "LAlt" | "RAlt" | "Win" | "LWin" | "RWin";
  /** @since 0.5 */
  export type CharKey = "A" | "B" | "C" | "D" | "E" | "F" | "G" | "H" | "I" | "J" | "K" | "L" | "M" | "N" | "O" | "P" | "Q" | "R" | "S" | "T" | "U" | "V" | "W" | "X" | "Y" | "Z"
    | "0" | "1" | "2" | "3" | "4" | "5" | "6" | "7" | "8" | "9" | "[" | "]" | ";" | "'" | "," | "." | "/" | "\\" | "-" | "=" | "`";
  /** @since 0.5 */
  export type NamedKey = `F${1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 | 11 | 12 | 13 | 14 | 15 | 16 | 17 | 18 | 19 | 20 | 21 | 22 | 23 | 24}`
    | "Space" | "Enter" | "Tab" | "Esc" | "Backspace" | "Delete" | "Insert" | "Home" | "End" | "PageUp" | "PageDown"
    | "Up" | "Down" | "Left" | "Right" | "Plus" | "CapsLock" | "NumLock" | "ScrollLock" | "PrintScreen" | "Pause" | "Menu"
    | `Num${0 | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9}` | "NumAdd" | "NumSubtract" | "NumMultiply" | "NumDivide" | "NumDecimal" | "NumEnter"
    | "IntlBackslash" | "IntlRo" | "IntlYen" | "Convert" | "NonConvert" | "KanaMode";
  /** @since 0.5 */
  export type PhysicalKey = `Key${CharKey}` | `sc:0x${string}`;
  /** 鼠标滚轮与鼠标按键，可作键名和组合键的主键，如 "Ctrl+WheelUp"、"Shift+MouseLeft"：滚轮按下滚一格，松开不发事件；按键像键一样按住、松开，按住时不会重复按下。别名 LButton、RButton、MButton、XButton1、XButton2 @since 0.8 */
  export type MouseKey = "WheelUp" | "WheelDown" | "WheelLeft" | "WheelRight"
    | "MouseLeft" | "MouseRight" | "MouseMiddle" | "MouseX1" | "MouseX2"
    | "LButton" | "RButton" | "MButton" | "XButton1" | "XButton2";
  /** 音量与媒体键，可作键名和组合键的主键，如 "VolumeUp"、"Ctrl+MediaNext"：和普通键一样点按、按住、连发，也能放进序列，不看键盘布局。别名是 W3C 的名字 AudioVolumeUp、AudioVolumeDown、AudioVolumeMute、MediaTrackNext、MediaTrackPrevious。运行时的 KeySpec 解析器在每个 API 版本都认这些名字，类型从 0.21 起 @since 0.21 */
  export type MediaKey = "VolumeUp" | "VolumeDown" | "VolumeMute" | "MediaPlayPause" | "MediaNext" | "MediaPrevious" | "MediaStop"
    | "AudioVolumeUp" | "AudioVolumeDown" | "AudioVolumeMute" | "MediaTrackNext" | "MediaTrackPrevious";
  /** @since 0.5 @until 0.8 */
  export type Key = CharKey | NamedKey | PhysicalKey | Modifier;
  /** @since 0.8 @until 0.21 */
  export type Key = CharKey | NamedKey | PhysicalKey | Modifier | MouseKey;
  /** @since 0.21 */
  export type Key = CharKey | NamedKey | PhysicalKey | Modifier | MouseKey | MediaKey;
  /** @since 0.5 */
  export type KeySpec = Key | `${Modifier}+${string}`;   // 运行时严格校验；不表示序列
  /** @since 0.5 */
  export interface TapResult { readonly dropped: boolean }
  /** `key.sequence` 的一步，与 profile 的 sequence 动作相同（SCR-26，ADR-0070）：`keys` 点按一个快捷键；`wait` 等待 1 至 10000 ms，从上一步的抬起算起（第一步从开始算）；`text` 输入一段文字，1 至 256 个 UTF-16 单元，换行点按 Enter，制表符点按 Tab，不接受别的控制字符。一步只写一个成员，一个序列 1 至 64 步 @since 0.21 */
  export type SequenceStep = { readonly keys: KeySpec; readonly wait?: never; readonly text?: never } | { readonly wait: number; readonly keys?: never; readonly text?: never } | { readonly text: string; readonly keys?: never; readonly wait?: never };
  /** @macro */
  export const key: {
    /** 数组表示依次 tap @since 0.5 */ tap(spec: KeySpec | readonly KeySpec[], o?: { holdMs?: number }): Promise<TapResult>;
    /** @since 0.5 */ down(spec: KeySpec): void;
    /** @since 0.5 */ up(spec: KeySpec): void;
    /** @since 0.5 */ isHeld(spec: KeySpec): boolean;
    /** 只释放本脚本持有的键 @since 0.5 */ releaseAll(): void;
    /** @since 0.5 */ parse(s: string): { ok: true; canonical: string } | { ok: false; error: string };
    /** 依次做每一步：点按快捷键、等待、输入文字（与 profile 的 sequence 动作相同）；全部做完或被取消时返回，被取消时 dropped 为 true @since 0.21 */ sequence(steps: readonly SequenceStep[]): Promise<TapResult>;
    /** 逐个字符输入一段文字，不看键盘布局，也绕过输入法；返回规则同 sequence @since 0.21 */ type(text: string): Promise<TapResult>;
  };
  /** @since 0.23 */ export function repeat(spec: KeySpec, o: { hz: number | (() => number); while: () => boolean }): Disposable;

  // ---- 时间与状态 ----
  /** @since 0.5 @macro */ export function now(): number;
  /** @since 0.5 @macro */ export function sleep(ms: number): Promise<void>;
  /** @since 0.5 */ export function every(ms: number, fn: () => void): Disposable;
  /** @since 0.6 */
  export interface KV { get<T = unknown>(k: string): T | undefined; set(k: string, v: unknown): void; delete(k: string): void }
  /** 内存中，跨 reload 与重新激活 @since 0.6 @macro */ export const session: KV;
  /** 持久化，每 profile 1 MB @since 0.23 */ export const store: KV;

  // ---- 环境 ----
  /** @since 0.16 */
  export const hud: {
    /** @since 0.16 */ toast(text: string, o?: { ms?: number }): void;
    /** @since 0.16 */ panel(id: string, content: { title?: string; rows: readonly [string, string][] } | null): void;
    /** M19，1.0 之后；API 编号待定，先标 0.22 @since 0.22 */ radial(o: { items: readonly string[]; stick: StickName; selected: number | null } | null): void;
  };
  /** @since 0.8 */ export interface AppInfo { readonly exe: string; readonly path: string; readonly pid: number; readonly elevated: boolean }
  /** @since 0.8 */ export const app: { readonly current: AppInfo | null; is(exe: string): boolean; onChange(h: (a: AppInfo | null) => void): Disposable };
  /** @since 0.8 */ export const profiles: { readonly current: string; list(): readonly string[]; switchTo(id: string): void };
  /** @since 0.5 @macro */ export const log: { debug(...a: unknown[]): void; info(...a: unknown[]): void; warn(...a: unknown[]): void; error(...a: unknown[]): void };
  /** 声明 "clipboard" 能力后可用；门控 @since 1.0，API @since 1.1 */
  /** @since 1.1 */
  export const clipboard: { readText(): string; writeText(t: string): void } | undefined;
  /** 声明 "exec" 能力后可用；门控 @since 1.0，API @since 1.1 */
  /** @since 1.1 */
  export const system: { run(path: string, args?: readonly string[]): void; open(target: string): void } | undefined;
}

/** @since 0.6 */
declare module "padmate/math" {
  export function clamp(v: number, lo: number, hi: number): number;
  export function lerp(a: number, b: number, t: number): number;
  export function remap(v: number, a0: number, a1: number, b0: number, b1: number): number;
  export function deadzone(v: number, inner: number, outer?: number): number;
  export function curve(v: number, kind: "linear" | "quadratic" | "cubic" | number): number;
  export function angle(x: number, y: number): number;               // 0deg = up, clockwise
  export function sector(angleDeg: number, sectors: number, offsetDeg?: number): number;
  export class Hysteresis { constructor(on: number, off: number); update(v: number): boolean }
  export class RateAccumulator { step(hz: number, dtMs: number): number; reset(): void }
  export class SectorTracker {
    constructor(sectors: number, o?: { hysteresisDeg?: number; offsetDeg?: number });
    update(angleDeg: number): number; reset(): void;
  }
}
/** @since 0.22 */
declare module "padmate/std" {
  /** M19，1.0 之后；API 编号待定，先标 0.22 @since 0.22 */ export function radialMenu(o: { stick: import("padmate").StickName; items: readonly { label: string; run: () => void }[]; openWith: import("padmate").Control }): import("padmate").Disposable;
  /** @since 0.23 */ export function cycle<T>(key: string, items: readonly T[], dir?: 1 | -1): T;   // 状态存于 session
  /** @since 0.23 */ export function stickRepeat(o: { stick: import("padmate").StickName; up: import("padmate").KeySpec; down: import("padmate").KeySpec; minHz: number; maxHz: number }): import("padmate").Disposable;
  /** @since 0.23 */ export function modifierButton(c: import("padmate").Control, spec: import("padmate").KeySpec): import("padmate").Disposable;
}
/** @since 0.23 */
declare module "padmate/test" {
  export interface Sim {
    press(c: import("padmate").Control, at?: number): void; release(c: import("padmate").Control, at?: number): void;
    tap(c: import("padmate").Control, holdMs?: number): void;
    stick(s: import("padmate").StickName, x: number, y: number): void; trigger(t: import("padmate").TriggerName, v: number): void;
    advance(ms: number): void; setApp(exe: string | null): void; setParam(name: string, v: unknown): void;
    readonly output: readonly { t: number; key: string; down: boolean }[];
    readonly hud: readonly unknown[]; readonly logs: readonly string[];
    expectKeys(...notation: string[]): void;          // "F13"、"Ctrl+Z"、"+LShift"/"-LShift"
    expectHeld(...keys: string[]): void; clear(): void;
  }
  export function test(name: string, fn: (sim: Sim) => void | Promise<void>,
    o?: { device?: string; params?: Record<string, unknown>; app?: string; layout?: string }): void;
  export function expect<T>(v: T): { toBe(x: T): void; toEqual(x: unknown): void; toBeTruthy(): void };
}
