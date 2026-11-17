# Памятка по TrueDSL

Одна страница. Подробности — в [справочнике](reference.md).

## Правила
- Отступ — **2 пробела** (без табуляции). Блок = строки со сдвигом.
- `#` — комментарий.
- Равенство — **`is`** (`if lives is 0`). Знак `=` — только присваивание.
- Нумерация списков — **с единицы**.
- Число в тексте — через `{ }`: `"Счёт: {score}"`.
- Условие всегда **да** (`yes`) / **нет** (`no`).

## События
```
when start                     when update
when key space pressed         when key space held         when key space released
when click                     when touch "tag"            when stop touching "tag"
when message "name"            when every 2 seconds
```
Внутри: `self` (я), `other` (второй при касании), `value` (данные сообщения).

## Переменные
```
var hp = 100        # у объекта      hp -= 5   hp += 1   hp *= 2   hp /= 2
shared score = 0    # общая для игры
```

## Условия и циклы
```
if a is b / is not / < / > / <= / >=      and  or  not      "x" in list
else if ...        else
repeat 3 times     repeat forever     while cond
for each item in list      for each i in 1 to 5
stop repeating     next round
```

## Команды
| | |
|---|---|
| `move right by 5` / `... per second` | `move left/up/down ...`, `avoiding "wall"` |
| `move to x, y` | `slide to x, y in 2 seconds` |
| `turn by 90` | `show` / `hide` |
| `spawn "Star" at x, y` | `destroy` / `destroy other` |
| `play sound "a.wav"` | `play music "m"` / `stop music` |
| `print "text"` | `wait 1 second` |
| `send "msg"` / `send "msg" with 5` / `send "msg" to Door` | |
| `go to scene "level2"` | `restart scene` |
| `remember "key" as value` | `recall("key", 0)` |
| `add x to list` | `remove x from list`, `clear list`, `sort list`, `shuffle list` |

## Свойства объекта
`x y width height angle visible image tint flip layer tag name text font_size` · у других объектов: `Hero.x`

## Функции
`abs min max round floor ceil sqrt sin cos random pick upper lower number find all count exists distance touching opposite recall`

## Свои команды
```
to heal(amount)
  hp = min(hp + amount, 100)
  return hp
```

## Частые ошибки
| Нельзя | Нужно |
|---|---|
| `if lives == 0` | `if lives is 0` |
| `wait` в `when update` | вынести в другой обработчик |
| `items[0]` | `items[1]` |
| `"Счёт: " + score` | `"Счёт: {score}"` |
| табуляция | два пробела |
