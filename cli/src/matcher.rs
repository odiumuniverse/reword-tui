//! The typed-answer check, ported one-to-one from ReWord 4.3.4: the answer
//! automaton (wl5 Matcher, st9 Transition, d23 ExecState), the split into
//! comma parts (h53.d), the per-language rules (o05 and its subclasses) and
//! the verdict WordCardView.b draws from them.
//!
//! Strings are handled as UTF-16 code units, the way Java sees them, so
//! positions, lengths and case mapping line up with the phone. Only the
//! CHECK search is ported; the HINT one feeds the phone's typing hint.

use std::collections::HashMap;

use anyhow::{Result, bail};
use unicode_normalization::UnicodeNormalization;
use unicode_normalization::char::is_combining_mark;
use unicode_properties::{GeneralCategory, GeneralCategoryGroup, UnicodeGeneralCategory};

/// How a typed answer came out, as WordCardView.b grades it.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Verdict {
    Wrong = 1,
    /// Accepted in yellow: loose letters (accents, ё/е) or only some meanings.
    Partial = 2,
    Correct = 3,
}

impl Verdict {
    /// The phone takes a partial answer as well as a correct one.
    pub fn accepted(self) -> bool {
        self != Verdict::Wrong
    }

    pub fn name(self) -> &'static str {
        match self {
            Verdict::Wrong => "wrong",
            Verdict::Partial => "partial",
            Verdict::Correct => "correct",
        }
    }
}

/// Checks a typed answer against the expected one in language `lang` (the
/// phone's three-letter code: the native language for a translation, the
/// course language for the word itself).
pub fn check(typed: &str, expected: &str, lang: &str) -> Result<Verdict> {
    let lang = Lang::of(lang)?;
    let typed = split(&normalize(typed));
    let parts = split(&normalize(expected));
    let matchers: Vec<Matcher> = parts.iter().map(|p| Matcher::new(p, &lang)).collect();
    let (mut strict, mut loose) = (0, 0);
    for t in &typed {
        for m in &matchers {
            let n = m.run(t);
            if n.at_end && n.prefix == 0 && n.suffix == 0 {
                if n.strict == Strictness::Loose {
                    loose += 1;
                } else {
                    strict += 1;
                }
                break;
            }
        }
    }
    Ok(
        if strict + loose == parts.len() && typed.len() == matchers.len() {
            if loose > 0 { Verdict::Partial } else { Verdict::Correct }
        } else if strict > 0 {
            Verdict::Partial
        } else {
            Verdict::Wrong
        },
    )
}

/// The course language an app asks the word itself in. ma3.d hard-codes it
/// per app flavour ("eng" in ReWord English); app ids start with the course
/// language's two-letter code ("en", "es", "esen").
pub fn course_lang(app: &str) -> Option<&'static str> {
    Some(match app.get(..2)? {
        "ar" => "ara",
        "cs" => "ces",
        "de" => "deu",
        "en" => "eng",
        "es" => "spa",
        "fi" => "fin",
        "fr" => "fra",
        "it" => "ita",
        "ja" => "jpn",
        "ka" => "kat",
        "ko" => "kor",
        "nl" => "nld",
        "pl" => "pol",
        "pt" => "por",
        "ru" => "rus",
        "tr" => "tur",
        "uk" => "ukr",
        "zh" => "zho",
        _ => return None,
    })
}

#[derive(Clone, Copy)]
enum Article {
    None,
    /// `^(?i:w1|w2…)(?=[\p{L}(])`, each word followed by `\s+` (true) or
    /// `\s*` (false).
    Words(&'static [(&'static str, bool)]),
    /// Japanese `^[~～〜](?=[\p{L}])`.
    Tilde,
}

/// One of the phone's o05 languages.
struct Lang {
    /// `String.toLowerCase` with a Turkish locale.
    turkic: bool,
    article: Article,
    /// Japanese `(?<=\p{L})[~～〜]$`.
    placeholder: bool,
    /// Letters that may also be typed as these (ё as е, ä as ae).
    spellings: &'static [(&'static str, &'static str)],
}

impl Lang {
    fn of(code: &str) -> Result<Lang> {
        let plain = Lang { turkic: false, article: Article::None, placeholder: false, spellings: &[] };
        Ok(match code.to_ascii_lowercase().as_str() {
            "ara" | "ces" | "fin" | "kat" | "kor" | "pol" | "ukr" | "zho" | "zhs" | "zht" => plain,
            "tur" => Lang { turkic: true, ..plain },
            "rus" => Lang { spellings: &[("ё", "е")], ..plain },
            "eng" => Lang {
                article: Article::Words(&[("a", true), ("an", true), ("the", true), ("to", true)]),
                ..plain
            },
            "spa" => Lang {
                article: Article::Words(&[
                    ("el", true),
                    ("la", true),
                    ("los", true),
                    ("las", true),
                    ("un", true),
                    ("una", true),
                    ("unos", true),
                    ("unas", true),
                ]),
                ..plain
            },
            "deu" => Lang {
                article: Article::Words(&[("der", true), ("die", true), ("das", true)]),
                spellings: &[("ä", "ae"), ("ö", "oe"), ("ü", "ue"), ("ß", "ss")],
                ..plain
            },
            "fra" => Lang {
                article: Article::Words(&[
                    ("le", true),
                    ("la", true),
                    ("l'", false),
                    ("l’", false),
                    ("les", true),
                    ("un", true),
                    ("une", true),
                    ("des", true),
                ]),
                spellings: &[("æ", "ae"), ("œ", "oe")],
                ..plain
            },
            "ita" => Lang {
                article: Article::Words(&[
                    ("il", true),
                    ("lo", true),
                    ("la", true),
                    ("l'", false),
                    ("l’", false),
                    ("gli", true),
                    ("i", true),
                    ("le", true),
                ]),
                ..plain
            },
            "nld" => Lang { article: Article::Words(&[("de", true), ("het", true), ("een", true)]), ..plain },
            "por" => Lang {
                article: Article::Words(&[
                    ("o", true),
                    ("a", true),
                    ("os", true),
                    ("as", true),
                    ("um", true),
                    ("uma", true),
                    ("uns", true),
                    ("umas", true),
                ]),
                ..plain
            },
            "jpn" => Lang { article: Article::Tilde, placeholder: true, ..plain },
            other => bail!("unknown language: {other}"),
        })
    }

    /// Where the article the language's pattern finds at the start ends.
    fn article_end(&self, s: &[u16]) -> Option<usize> {
        match self.article {
            Article::None => None,
            Article::Tilde => {
                (s.first().is_some_and(|&u| is_tilde(u)) && code_point_at(s, 1).is_some_and(is_letter)).then_some(1)
            }
            // Only one alternative can fit a given start, and the lookahead
            // sees past every space `\s+` could take, so no backtracking.
            Article::Words(words) => words.iter().find_map(|&(w, spaced)| {
                let w: Vec<u16> = w.encode_utf16().collect();
                if s.len() < w.len() || !s.iter().zip(&w).all(|(&a, &b)| ascii_eq_ignore_case(a, b)) {
                    return None;
                }
                let mut end = w.len();
                while end < s.len() && is_regex_space(s[end]) {
                    end += 1;
                }
                if spaced && end == w.len() {
                    return None;
                }
                code_point_at(s, end).is_some_and(|c| c == '(' || is_letter(c)).then_some(end)
            }),
        }
    }

    /// Where the trailing `~` the placeholder pattern finds sits.
    fn placeholder_at(&self, s: &[u16]) -> Option<usize> {
        if !self.placeholder {
            return None;
        }
        // Java's `$` holds at the end, or before one final line terminator.
        let n = s.len();
        let mut ends = Vec::new();
        if n >= 2 && s[n - 2] == 0x0D && s[n - 1] == 0x0A {
            ends.push(n - 2);
        }
        if n >= 1 && is_line_terminator(s[n - 1]) && !(s[n - 1] == 0x0A && n >= 2 && s[n - 2] == 0x0D) {
            ends.push(n - 1);
        }
        ends.push(n);
        ends.into_iter()
            .filter_map(|e| e.checked_sub(1))
            .find(|&p| is_tilde(s[p]) && code_point_before(s, p).is_some_and(is_letter))
    }

    fn spelling(&self, key: &[u16]) -> Option<&'static str> {
        self.spellings.iter().find(|(k, _)| k.encode_utf16().eq(key.iter().copied())).map(|&(_, v)| v)
    }
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum Kind {
    WrongPrefix,
    WrongSuffix,
    EpsArticle,
    EpsParens,
    EpsPlaceholder,
    Str,
}

impl Kind {
    fn is_eps(self) -> bool {
        matches!(self, Kind::EpsArticle | Kind::EpsParens | Kind::EpsPlaceholder)
    }
}

struct Trans {
    kind: Kind,
    to: usize,
    /// What the step reads, lower-cased (st9.d).
    text: Vec<u16>,
    /// A loose spelling: the accent-free letter or a language spelling.
    loose: bool,
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum Strictness {
    Strict,
    Loose,
    None,
}

/// A path through the automaton (d23).
#[derive(Clone, Debug)]
struct Node {
    state: usize,
    at_end: bool,
    strict: Strictness,
    /// Characters thrown away before the answer.
    prefix: usize,
    /// Characters the answer matched.
    len: usize,
    /// Characters thrown away after it.
    suffix: usize,
}

impl Node {
    fn step(&self, t: &Trans, at_end: bool, strict: Strictness) -> Node {
        Node {
            state: t.to,
            at_end,
            strict: if self.strict == Strictness::Loose { Strictness::Loose } else { strict },
            prefix: self.prefix + usize::from(t.kind == Kind::WrongPrefix),
            len: self.len + if t.kind == Kind::Str { t.text.len() } else { 0 },
            suffix: self.suffix + usize::from(t.kind == Kind::WrongSuffix),
        }
    }

    /// d23.a in CHECK mode: at the end first, then the longer match, then
    /// the strict one; a tie goes to the newcomer.
    fn beats(&self, o: &Node) -> bool {
        if self.at_end != o.at_end {
            return self.at_end;
        }
        if self.len != o.len {
            return self.len > o.len;
        }
        self.strict == Strictness::Strict && o.strict != Strictness::Strict
    }
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum SpanKind {
    Article,
    Parens,
    Placeholder,
}

/// A stretch of the answer that may be left out.
#[derive(Clone, Copy)]
struct Span {
    a: usize,
    b: usize,
    kind: SpanKind,
}

/// The automaton for one comma part of the answer (wl5).
struct Matcher {
    start: usize,
    end: usize,
    states: usize,
    out: Vec<Vec<Trans>>,
    turkic: bool,
}

impl Matcher {
    fn new(text: &[u16], lang: &Lang) -> Matcher {
        let mut m = Matcher { start: 0, end: 0, states: 1, out: Vec::new(), turkic: lang.turkic };
        if text.is_empty() {
            return m;
        }
        let n = text.len();
        let stripped = match strip_marks(text) {
            s if s.len() == n => s,
            _ => text.to_vec(),
        };
        m.add(Kind::WrongPrefix, 0, 0, &[], false);

        let mut spans: Vec<Span> = Vec::new();
        let mut i = 0;
        if let Some(end) = lang.article_end(text)
            && !is_only_parens(&text[end..])
        {
            spans.push(Span { a: 0, b: end - 1, kind: SpanKind::Article });
            i = end;
        }
        let mut depth = 0i32;
        let mut open = 0;
        let mut balanced = true;
        while i < n {
            let ch = text[i];
            if ch == u16::from(b'(') || ch == 0xFF08 {
                depth += 1;
                if depth == 1 {
                    // The spaces before a note go with it.
                    open = i;
                    while open > 0
                        && is_java_whitespace(text[open - 1])
                        && spans.last().is_none_or(|s| s.b < open - 1)
                    {
                        open -= 1;
                    }
                }
            } else if ch == u16::from(b')') || ch == 0xFF09 {
                depth -= 1;
                if depth < 0 {
                    balanced = false;
                    break;
                }
                if depth == 0 {
                    while i < n - 1 && is_java_whitespace(text[i + 1]) {
                        i += 1;
                    }
                    spans.push(Span { a: open, b: i, kind: SpanKind::Parens });
                }
            }
            i += 1;
        }
        if balanced && depth == 0 {
            if let Some(p) = lang.placeholder_at(text) {
                spans.push(Span { a: p, b: p, kind: SpanKind::Placeholder });
            }
        } else {
            spans.clear();
        }

        let mut entry = m.start;
        for i in 0..n {
            if spans.first().is_some_and(|s| s.a == i) {
                entry = m.end;
            }
            let (ch, plain) = (text[i], stripped[i]);
            let next = m.new_state();
            let from = m.end;
            m.add(Kind::Str, from, next, &[ch], false);
            if ch != plain {
                m.add(Kind::Str, from, next, &[plain], true);
            }
            if let Some(alt) = lang.spelling(&lower(&[ch], lang.turkic)) {
                let alt: Vec<u16> = alt.encode_utf16().collect();
                let mut at = from;
                for (k, &u) in alt.iter().enumerate() {
                    let to = if k == alt.len() - 1 { next } else { m.new_state() };
                    m.add(Kind::Str, at, to, &[u], true);
                    at = to;
                }
            }
            m.end = next;
            if let Some(&s) = spans.first()
                && s.b == i
            {
                let kind = match s.kind {
                    SpanKind::Article => Kind::EpsArticle,
                    SpanKind::Parens => Kind::EpsParens,
                    SpanKind::Placeholder => Kind::EpsPlaceholder,
                };
                m.add(kind, entry, next, &[], false);
                if s.a > 0 && s.b < n - 1 {
                    // Left out mid-answer, it leaves one space behind.
                    m.add(Kind::Str, entry, next, &[0x20], false);
                }
                spans.remove(0);
            }
        }
        let end = m.end;
        m.add(Kind::WrongSuffix, end, end, &[], false);
        m
    }

    fn new_state(&mut self) -> usize {
        self.states += 1;
        self.states - 1
    }

    fn add(&mut self, kind: Kind, from: usize, to: usize, text: &[u16], loose: bool) {
        if self.out.len() <= from {
            self.out.resize_with(from + 1, Vec::new);
        }
        let text = if kind == Kind::Str { lower(text, self.turkic) } else { Vec::new() };
        self.out[from].push(Trans { kind, to, text, loose });
    }

    fn from(&self, state: usize) -> &[Trans] {
        self.out.get(state).map_or(&[], Vec::as_slice)
    }

    /// wl5.e in CHECK mode: the best path for `input`, layer by layer.
    fn run(&self, input: &[u16]) -> Node {
        let root = Node {
            state: self.start,
            at_end: self.start == self.end,
            strict: Strictness::Strict,
            prefix: 0,
            len: 0,
            suffix: 0,
        };
        let mut queue = vec![self.start];
        let mut best = HashMap::from([(self.start, root)]);
        self.closure(&mut queue, &mut best);
        let mut stuck: Option<Node> = None;
        while !queue.is_empty() {
            let mut layer = JavaMap::new();
            while let Some(s) = queue.pop() {
                let node = best[&s].clone();
                for t in self.from(node.state) {
                    let got = self.read(t, input, node.prefix + node.len + node.suffix);
                    if got == Strictness::None {
                        if stuck.as_ref().is_none_or(|b| node.beats(b)) {
                            stuck = Some(node.clone());
                        }
                        continue;
                    }
                    let next = node.step(t, t.to == self.end, got);
                    if layer.get(t.to).is_some_and(|n| n.beats(&next)) {
                        continue;
                    }
                    layer.put(t.to, next);
                }
            }
            for n in layer.into_values() {
                if best.get(&n.state).is_some_and(|b| b.beats(&n)) {
                    continue;
                }
                queue.push(n.state);
                best.insert(n.state, n);
            }
            self.closure(&mut queue, &mut best);
        }
        best.get(&self.end).cloned().or(stuck).unwrap_or_else(|| best[&self.start].clone())
    }

    /// wl5.b: follows the steps that read nothing (a left-out stretch).
    fn closure(&self, queue: &mut Vec<usize>, best: &mut HashMap<usize, Node>) {
        let mut work = queue.clone();
        while let Some(s) = work.pop() {
            for t in self.from(s) {
                if !t.kind.is_eps() {
                    continue;
                }
                let src = best[&s].clone();
                let next = src.step(t, t.to == self.end, src.strict);
                if best.get(&t.to).is_some_and(|b| b.beats(&next)) {
                    continue;
                }
                if !queue.contains(&t.to) {
                    queue.push(t.to);
                }
                best.insert(t.to, next);
                work.push(t.to);
            }
        }
    }

    /// How step `t` reads `input` at `pos`.
    fn read(&self, t: &Trans, input: &[u16], pos: usize) -> Strictness {
        match t.kind {
            Kind::EpsArticle | Kind::EpsParens | Kind::EpsPlaceholder => Strictness::None,
            Kind::WrongPrefix | Kind::WrongSuffix => {
                if input.len() < pos + 1 { Strictness::None } else { Strictness::Strict }
            }
            Kind::Str => {
                if input.len() < pos + t.text.len() {
                    return Strictness::None;
                }
                let sub = lower(&input[pos..pos + t.text.len()], self.turkic);
                if sub == t.text {
                    return if t.loose { Strictness::Loose } else { Strictness::Strict };
                }
                if PUNCTUATION.iter().any(|g| in_group(g, &sub) && in_group(g, &t.text)) {
                    return Strictness::Strict;
                }
                if strip_marks(&sub) == t.text { Strictness::Loose } else { Strictness::None }
            }
        }
    }
}

/// Punctuation that stands in for each other (st9.h).
const PUNCTUATION: [&str; 10] = [
    ",，、;",
    "'’‘\"「」『』«»",
    "~〜～",
    "!¡！",
    "?？¿",
    ".。…‥",
    ":：",
    "｛（［【([{",
    "｝）］】)]}",
    "-‐−–—",
];

fn in_group(group: &str, s: &[u16]) -> bool {
    s.len() == 1 && group.encode_utf16().any(|u| u == s[0])
}

/// A `java.util.HashMap<Integer, _>` just far enough to hand its values out
/// in Java's order — by bucket, then by insertion — since equally good paths
/// are settled by that order on the phone. (Tree bins, which reorder a bucket,
/// only form past 64 buckets with 8 states in one: not in a word.)
struct JavaMap {
    keys: Vec<usize>,
    vals: Vec<Node>,
    cap: usize,
}

impl JavaMap {
    fn new() -> JavaMap {
        JavaMap { keys: Vec::new(), vals: Vec::new(), cap: 16 }
    }

    fn bucket(key: usize, cap: usize) -> usize {
        let h = key as u32;
        ((h ^ (h >> 16)) as usize) & (cap - 1)
    }

    fn get(&self, key: usize) -> Option<&Node> {
        self.keys.iter().position(|&k| k == key).map(|i| &self.vals[i])
    }

    fn put(&mut self, key: usize, val: Node) {
        if let Some(i) = self.keys.iter().position(|&k| k == key) {
            self.vals[i] = val;
            return;
        }
        let b = Self::bucket(key, self.cap);
        let chain = self.keys.iter().filter(|&&k| Self::bucket(k, self.cap) == b).count();
        self.keys.push(key);
        self.vals.push(val);
        if chain >= 8 && self.cap < 64 {
            // treeifyBin grows a small table instead.
            self.cap *= 2;
        }
        if self.keys.len() > self.cap * 3 / 4 {
            self.cap *= 2;
        }
    }

    fn into_values(self) -> Vec<Node> {
        let cap = self.cap;
        let mut order: Vec<usize> = (0..self.keys.len()).collect();
        order.sort_by_key(|&i| Self::bucket(self.keys[i], cap));
        let mut vals: Vec<Option<Node>> = self.vals.into_iter().map(Some).collect();
        order.into_iter().filter_map(|i| vals[i].take()).collect()
    }
}

/// The driver's clean-up: dashes to spaces, each run of `\s` to one space,
/// then `String.trim()`.
fn normalize(s: &str) -> Vec<u16> {
    let mut out: Vec<u16> = Vec::with_capacity(s.len());
    let mut spaced = false;
    for u in s.encode_utf16() {
        let u = if in_group(PUNCTUATION[9], &[u]) { 0x20 } else { u };
        if is_regex_space(u) {
            if !spaced {
                out.push(0x20);
            }
            spaced = true;
        } else {
            out.push(u);
            spaced = false;
        }
    }
    let lo = out.iter().position(|&u| u > 0x20).unwrap_or(out.len());
    let hi = out.iter().rposition(|&u| u > 0x20).map_or(lo, |i| i + 1);
    out[lo..hi].to_vec()
}

fn is_comma(u: u16) -> bool {
    matches!(u, 0x2C | 0xFF0C | 0x3001)
}

/// h53.d: the comma parts of an answer, commas inside brackets kept. (Java's
/// `$` before a final U+0085/U+2028/U+2029 is not followed: the driver has
/// already turned every other line break into a space.)
fn split(s: &[u16]) -> Vec<Vec<u16>> {
    if s.is_empty() {
        return vec![Vec::new()];
    }
    // `^(\s*[,，、]\s*)+|(\s*[,，、]\s*)+$` → "".
    let mut lo = 0;
    loop {
        let mut j = lo;
        while j < s.len() && is_regex_space(s[j]) {
            j += 1;
        }
        if j >= s.len() || !is_comma(s[j]) {
            break;
        }
        j += 1;
        while j < s.len() && is_regex_space(s[j]) {
            j += 1;
        }
        lo = j;
    }
    let mut hi = s.len();
    let mut k = s.len();
    let mut comma = false;
    while k > lo && (is_regex_space(s[k - 1]) || is_comma(s[k - 1])) {
        k -= 1;
        comma |= is_comma(s[k]);
    }
    if comma {
        hi = k;
    }
    let s = &s[lo..hi];

    let mut parts = Vec::new();
    let (mut depth, mut start, mut count) = (0i32, 0, 0);
    for (i, &ch) in s.iter().enumerate() {
        if ch == u16::from(b'(') || ch == 0xFF08 {
            depth += 1;
        } else if ch == u16::from(b')') || ch == 0xFF09 {
            depth -= 1;
        }
        let comma = is_comma(ch);
        if (!comma || depth > 0) && i != s.len() - 1 {
            count += 1;
            continue;
        }
        let mut part = &s[start..start + count + usize::from(!comma)];
        while part.first().is_some_and(|&u| is_regex_space(u)) {
            part = &part[1..];
        }
        while part.last().is_some_and(|&u| is_regex_space(u)) {
            part = &part[..part.len() - 1];
        }
        parts.push(part.to_vec());
        start = i + 1;
        depth = 0;
        count = 0;
    }
    parts
}

/// `rest.matches("^\\(.+\\)$")`.
fn is_only_parens(rest: &[u16]) -> bool {
    rest.len() >= 3
        && rest[0] == u16::from(b'(')
        && rest[rest.len() - 1] == u16::from(b')')
        && !rest[1..rest.len() - 1].iter().any(|&u| is_line_terminator(u))
}

/// `String.toLowerCase(locale)`; a Turkish locale maps I to ı and İ to i.
fn lower(s: &[u16], turkic: bool) -> Vec<u16> {
    let mut out = Vec::with_capacity(s.len());
    let mut run = String::new();
    let flush = |run: &mut String, out: &mut Vec<u16>| {
        out.extend(run.to_lowercase().encode_utf16());
        run.clear();
    };
    let mut chars = char::decode_utf16(s.iter().copied()).peekable();
    while let Some(r) = chars.next() {
        match r {
            Ok(c @ ('I' | 'İ')) if turkic => {
                flush(&mut run, &mut out);
                if c == 'I' && chars.peek().is_some_and(|n| *n == Ok('\u{307}')) {
                    chars.next();
                    out.push(u16::from(b'i'));
                } else if c == 'I' {
                    out.push(0x131);
                } else {
                    out.push(u16::from(b'i'));
                }
            }
            Ok(c) => run.push(c),
            Err(e) => {
                flush(&mut run, &mut out);
                out.push(e.unpaired_surrogate());
            }
        }
    }
    flush(&mut run, &mut out);
    out
}

/// `Normalizer.normalize(s, NFD).replaceAll("\\p{M}", "")`.
fn strip_marks(s: &[u16]) -> Vec<u16> {
    let mut out = Vec::with_capacity(s.len());
    let mut run = String::new();
    let flush = |run: &mut String, out: &mut Vec<u16>| {
        let mut buf = [0u16; 2];
        for c in run.nfd().filter(|&c| !is_combining_mark(c)) {
            out.extend_from_slice(c.encode_utf16(&mut buf));
        }
        run.clear();
    };
    for r in char::decode_utf16(s.iter().copied()) {
        match r {
            Ok(c) => run.push(c),
            Err(e) => {
                flush(&mut run, &mut out);
                out.push(e.unpaired_surrogate());
            }
        }
    }
    flush(&mut run, &mut out);
    out
}

/// Java regex `\s`: ASCII whitespace only.
fn is_regex_space(u: u16) -> bool {
    matches!(u, 0x20 | 0x09..=0x0D)
}

/// `Character.isWhitespace(char)`.
fn is_java_whitespace(u: u16) -> bool {
    match u {
        0x09..=0x0D | 0x1C..=0x1F => true,
        0xA0 | 0x2007 | 0x202F => false,
        _ => char::from_u32(u32::from(u)).is_some_and(|c| {
            matches!(
                c.general_category(),
                GeneralCategory::SpaceSeparator | GeneralCategory::LineSeparator | GeneralCategory::ParagraphSeparator
            )
        }),
    }
}

/// What Java's `.` stops at and `$` may stand before.
fn is_line_terminator(u: u16) -> bool {
    matches!(u, 0x0A | 0x0D | 0x85 | 0x2028 | 0x2029)
}

fn is_tilde(u: u16) -> bool {
    matches!(u, 0x7E | 0xFF5E | 0x301C)
}

fn is_letter(c: char) -> bool {
    c.general_category_group() == GeneralCategoryGroup::Letter
}

fn ascii_eq_ignore_case(a: u16, b: u16) -> bool {
    a == b || (a < 0x80 && b < 0x80 && (a as u8).eq_ignore_ascii_case(&(b as u8)))
}

fn code_point_at(s: &[u16], i: usize) -> Option<char> {
    char::decode_utf16(s.get(i..)?.iter().copied()).next()?.ok()
}

fn code_point_before(s: &[u16], i: usize) -> Option<char> {
    let lo = *s.get(i.checked_sub(1)?)?;
    if (0xDC00..0xE000).contains(&lo) && i >= 2 && (0xD800..0xDC00).contains(&s[i - 2]) {
        return char::decode_utf16([s[i - 2], lo]).next()?.ok();
    }
    char::from_u32(u32::from(lo))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn v(typed: &str, expected: &str, lang: &str) -> Verdict {
        check(typed, expected, lang).unwrap()
    }

    #[test]
    fn exact_and_case_are_correct() {
        assert_eq!(v("кошка", "кошка", "rus"), Verdict::Correct);
        assert_eq!(v("КОШКА", "кошка", "rus"), Verdict::Correct);
        assert_eq!(v("  the   Cat ", "the cat", "eng"), Verdict::Correct);
    }

    #[test]
    fn articles_and_notes_may_be_left_out() {
        assert_eq!(v("cat", "the cat", "eng"), Verdict::Correct);
        assert_eq!(v("run", "to run", "eng"), Verdict::Correct);
        assert_eq!(v("go", "(to) go", "eng"), Verdict::Correct);
        assert_eq!(v("бежать", "бежать (быстро)", "rus"), Verdict::Correct);
        assert_eq!(v("бежать (быстро)", "бежать (быстро)", "rus"), Verdict::Correct);
        // A note goes whole or not at all: its brackets are letters to type.
        assert_eq!(v("бежать быстро", "бежать (быстро)", "rus"), Verdict::Wrong);
        assert_eq!(v("take off", "to take (something) off", "eng"), Verdict::Correct);
        assert_eq!(v("gato", "el gato", "spa"), Verdict::Correct);
        assert_eq!(v("homme", "l'homme", "fra"), Verdict::Correct);
        // Only notes: the article stays.
        assert_eq!(v("x", "the (x)", "eng"), Verdict::Wrong);
    }

    #[test]
    fn loose_letters_are_partial() {
        assert_eq!(v("елка", "ёлка", "rus"), Verdict::Partial);
        assert_eq!(v("cancion", "canción", "spa"), Verdict::Partial);
        assert_eq!(v("strasse", "die Straße", "deu"), Verdict::Partial);
        assert_eq!(v("coeur", "cœur", "fra"), Verdict::Partial);
    }

    #[test]
    fn punctuation_stands_in_for_its_kind() {
        assert_eq!(v("l’homme", "l'homme", "fra"), Verdict::Correct);
        assert_eq!(v("что то", "что-то", "rus"), Verdict::Correct);
        assert_eq!(v("привет！", "привет!", "rus"), Verdict::Correct);
    }

    #[test]
    fn meanings_count_one_by_one() {
        assert_eq!(v("кот", "кот, кошка", "rus"), Verdict::Partial);
        assert_eq!(v("кошка, кот", "кот, кошка", "rus"), Verdict::Correct);
        assert_eq!(v("кот, собака", "кот, кошка", "rus"), Verdict::Partial);
        assert_eq!(v("собака", "кот, кошка", "rus"), Verdict::Wrong);
    }

    #[test]
    fn stray_letters_are_wrong() {
        assert_eq!(v("xкошка", "кошка", "rus"), Verdict::Wrong);
        assert_eq!(v("кошкаx", "кошка", "rus"), Verdict::Wrong);
        assert_eq!(v("кошк", "кошка", "rus"), Verdict::Wrong);
        assert_eq!(v("", "кошка", "rus"), Verdict::Wrong);
    }

    #[test]
    fn turkish_lowers_its_own_way() {
        assert_eq!(v("istanbul", "İstanbul", "tur"), Verdict::Correct);
        assert_eq!(v("ılık", "ILIK", "tur"), Verdict::Correct);
    }

    #[test]
    fn unknown_language_is_an_error() {
        assert!(check("a", "a", "xxx").is_err());
    }

    #[test]
    fn java_map_hands_values_out_by_bucket() {
        let node = |state| Node { state, at_end: false, strict: Strictness::Strict, prefix: 0, len: 0, suffix: 0 };
        let mut m = JavaMap::new();
        for k in [17, 3, 1, 33, 16] {
            m.put(k, node(k));
        }
        let got: Vec<usize> = m.into_values().iter().map(|n| n.state).collect();
        assert_eq!(got, vec![16, 17, 1, 33, 3]);
    }

    mod golden {
        use super::super::*;
        use serde::Deserialize;

        #[derive(Deserialize)]
        struct Case {
            lang: String,
            expected: String,
            typed: String,
            verdict: u8,
            states: Vec<usize>,
            runs: Vec<[usize; 8]>,
        }

        /// Vectors from a Java transcription of the phone's matcher, run on the
        /// JVM (its HashMap, regex, Normalizer and toLowerCase).
        #[test]
        fn matches_the_phone() {
            let mut bad = Vec::new();
            let mut n = 0;
            for line in include_str!("testdata/matcher_vectors.jsonl").lines() {
                let c: Case = serde_json::from_str(line).unwrap();
                n += 1;
                let lang = Lang::of(&c.lang).unwrap();
                let parts = split(&normalize(&c.expected));
                let typed = split(&normalize(&c.typed));
                let matchers: Vec<Matcher> = parts.iter().map(|p| Matcher::new(p, &lang)).collect();
                let states: Vec<usize> = matchers.iter().map(|m| m.states).collect();
                let mut runs = Vec::new();
                for (ti, t) in typed.iter().enumerate() {
                    for (mi, m) in matchers.iter().enumerate() {
                        let r = m.run(t);
                        let strict = match r.strict {
                            Strictness::Strict => 0,
                            Strictness::Loose => 1,
                            Strictness::None => 2,
                        };
                        runs.push([ti, mi, r.state, usize::from(r.at_end), strict, r.prefix, r.len, r.suffix]);
                    }
                }
                let verdict = check(&c.typed, &c.expected, &c.lang).unwrap() as u8;
                if verdict != c.verdict || states != c.states || runs != c.runs {
                    bad.push(format!(
                        "{} {:?} typed {:?}: verdict {verdict} want {}, states {states:?} want {:?}, runs {runs:?} want {:?}",
                        c.lang, c.expected, c.typed, c.verdict, c.states, c.runs
                    ));
                }
            }
            assert!(n > 1000, "only {n} vectors");
            assert!(bad.is_empty(), "{} of {n} differ:\n{}", bad.len(), bad.iter().take(15).cloned().collect::<Vec<_>>().join("\n"));
        }
    }
}
