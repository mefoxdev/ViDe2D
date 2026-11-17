# Примеры

Пять небольших скриптов. Для каждого указано, к каким объектам сцены он прикрепляется. Читайте вслух — так проще всего понять.

> Названия вроде `"coin"` и `"floor"` — это **метки** (`tag`), которые объектам назначают в студии. Звуки и картинки берутся из папки `assets` проекта.

---

## 1. Герой ходит (сверху вниз)

Скрипт для объекта `Hero`.

```
var speed = 70

when key left held
  move left by speed per second
  flip = yes

when key right held
  move right by speed per second
  flip = no

when key up held
  move up by speed per second

when key down held
  move down by speed per second
```

Нельзя выйти за экран? Добавьте проверку:

```
when update
  x = max(0, min(x, 304))
  y = max(0, min(y, 164))
```

---

## 2. Coin Dash — собери все монеты

Объекты: `Hero`, пять объектов-монет с меткой `coin`, надпись `Score` (объект Text).

**hero.tdsl**

```
var speed = 80

when key left held
  move left by speed per second
  flip = yes

when key right held
  move right by speed per second
  flip = no

when key up held
  move up by speed per second

when key down held
  move down by speed per second

when touch "coin"
  destroy other
  play sound "coin.wav"
  send "coin collected"
```

**coin.tdsl** (на каждой монете — чтобы она слегка «дышала»)

```
when every 1 second
  move up by 2
  wait 0.5 seconds
  move down by 2
```

**score.tdsl** (на надписи `Score`)

```
shared coins = 0

when start
  text = "Монет: 0 из 5"

when message "coin collected"
  coins += 1
  text = "Монет: {coins} из 5"
  if coins is 5
    send "win"

when message "win"
  text = "Ты победил!"
  wait 3 seconds
  go to scene "menu"
```

Как это работает: герой не знает про счётчик. Он только говорит «монета собрана». Счётчик слушает и считает.

---

## 3. Платформер: прыжок и гравитация

Объект `Hero`; земля и стены — объекты с меткой `floor`.

```
var vy = 0                      # вертикальная скорость

when key left held
  move left by 100 per second avoiding "floor"
  flip = yes

when key right held
  move right by 100 per second avoiding "floor"
  flip = no

when key space pressed
  if touching("floor", down)    # стою на земле?
    vy = -280
    play sound "jump.wav"

when update
  vy += 800 per second          # гравитация
  move down by vy per second avoiding "floor"
  if touching("floor", down) and vy > 0
    vy = 0
```

`avoiding "floor"` значит «не заходи в объекты с меткой `floor`». `touching("floor", down)` — «снизу вплотную стоит `floor`». Отрицательная скорость `vy` двигает вверх.

---

## 4. Враг-патрульный

Объект `Slime` с меткой `danger`; стены с меткой `wall`.

```
var heading = right

when update
  move heading by 40 per second avoiding "wall"
  if touching("wall", heading)
    heading = opposite(heading)
    flip = heading is left
```

Направление — полноценное значение: его можно положить в переменную, развернуть через `opposite` и сравнить через `is`.

Герой может умирать от касания:

```
# в скрипте героя
when touch "danger"
  send "hero died"
```

---

## 5. Падающие звёзды (с рекордом)

Объекты: `Hero`, невидимый `Spawner`, шаблон `Star` (метка `danger`), надпись `Board`.

**spawner.tdsl** (на `Spawner`)

```
when every 1 second
  spawn "Star" at random(10, 300), 0
```

**star.tdsl** (на шаблоне `Star`)

```
when update
  move down by 90 per second
  if y > 180
    destroy
```

**hero.tdsl**

```
when key left held
  move left by 120 per second

when key right held
  move right by 120 per second

when touch "danger"
  destroy other
  lives -= 1
  send "refresh"
  if lives is 0
    send "game over"
```

**board.tdsl** (на надписи `Board`)

```
shared lives = 3
shared score = 0
shared best = 0
var playing = yes

to show_info()
  text = "Жизни: {lives}   Очки: {score}   Рекорд: {best}"

when start
  best = recall("best", 0)
  show_info()

when every 1 second
  if playing
    score += 1
    show_info()

when message "refresh"
  show_info()

when message "game over"
  playing = no
  if score > best
    remember "best" as score
  text = "Игра окончена! Очки: {score}"
  wait 3 seconds
  restart scene
```

Что здесь нового: **шаблон** (`spawn`), **свои команды** (`show_info`), **память между запусками** (`remember` / `recall`) и **пауза** (`wait`) перед перезапуском.
