//go:build windows
// +build windows

package main

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/pkg/errors"

	"github.com/lxn/win"
)

func main() {
	mine, err := NewMine()
	if err != nil {
		fmt.Println(err)
		return
	}

	for {
		err = mine.Play()
		switch {
		case err == WinErr:
			fmt.Printf("你赢了比赛\n\n")
		case errors.Is(err, FailErr):
			fmt.Printf("你输了比赛: %v\n\n", err)
		default:
			fmt.Printf("出现错误: %+v\n\n", err)
		}

		fmt.Println("y 继续, n 退出:")
		if mine.ReadKey.WaitKeyboard('Y', 'N') == 'N' {
			return
		}
	}
}

const (
	GameGridLen = 16 // 每个格子长宽像素
	GameHigh    = 16 // 雷区高度,由于使用图像识别,只支持高级这种模式
	GameWide    = 30 // 雷区宽度
)

type Mine struct {
	// 笑脸的位置,点击可以开始游戏
	StartBtn win.POINT
	// 保存数据的二维数组
	GridSave [][]GridDefine
	// 保存界面上剩余雷的个数
	CountMine int
	// 扫雷窗体对象
	GameHWND win.HWND
	// 教学模式
	TeachMode bool
	// 人工猜雷
	ManualMine bool
	// 已经猜过一次雷
	AlreadyGuessed bool
	// true表示已经开局
	StartFlag bool
	// 不确定点列表
	NotSurePos [][2]int
	// 获取按键事件
	ReadKey *KeyBoard
}

func NewMine() (*Mine, error) {
	name := win.StringToBSTR("扫雷")
	// 找到扫雷窗体句柄
	hw := win.FindWindow(name, name)
	if hw == 0 {
		return nil, errors.New("请打开扫雷游戏")
	}

	rand.Seed(time.Now().UnixNano())
	m := &Mine{
		GameHWND:   hw,
		GridSave:   make([][]GridDefine, GameHigh),
		NotSurePos: make([][2]int, 0, GameHigh*GameWide),
		ReadKey:    NewKeyBoard(),
	}
	for i := 0; i < GameHigh; i++ {
		m.GridSave[i] = make([]GridDefine, GameWide)
	}

	/* 将开始按钮位置从窗体相对位置,转换为整个屏幕相对位置 */
	m.StartBtn = win.POINT{X: 250, Y: 25}
	win.ClientToScreen(m.GameHWND, &m.StartBtn)

	fmt.Println("y [教学模式],n [自动模式]")
	m.TeachMode = m.ReadKey.WaitKeyboard('Y', 'N') == 'Y'
	if !m.TeachMode {
		// 自动模式时需要选择自动猜雷火人工猜雷
		fmt.Println("y [人工猜雷],n [自动猜雷]")
		m.ManualMine = m.ReadKey.WaitKeyboard('Y', 'N') == 'Y'
	}
	return m, nil
}

// Reset 重新开始游戏时,重置参数
func (m *Mine) Reset() {
	for i := 0; i < GameHigh; i++ {
		for j := 0; j < GameWide; j++ {
			m.GridSave[i][j] = DefClick
		}
	}
	m.StartFlag = false
}

/*Play
扫雷算法
	1. X格子周围可点击数量 + X格子周围已标记雷数量 = X格子显示数值
		用右键将所有可点击位置标雷
	2. X格子周围已标记雷数量 = X格子显示数值
		用中键点击X格子,点开非雷区域
	3. 两个相邻格子待标雷数多的为A,待标雷数少的为B
		A待标雷数 - B待标雷数 = A可点击数 - AB 相交位置数
		A除相交位置以外的可点击位置全是雷,用右键全部标记为地雷
	4. 两个相邻格子A和B待标雷数量相同,A的可点击数为a,B的可点击数为b,相交格子数为c
		如果a = c,则格子B除开相交位置的可点击位置一定不是雷
		如果b = c,则格子A除开相交位置的可点击位置一定不是雷
	5. todo 发现更精确扫雷方法
	6. todo 发现更精确猜雷方法
*/
func (m *Mine) Play() (err error) {
	// 重新开局复位属性
	m.Reset()
	// 双击笑脸
	m.DoubleClickStart()

	for {
		err = m.RefreshGrid()
		if err != nil {
			return err
		}

		notSureFlag := true
		m.NotSurePos = m.NotSurePos[:0]
		for i := 0; i < GameHigh; i++ {
			for j := 0; j < GameWide; j++ {
				/* 只处理代表数字的位置 */
				if m.GridSave[i][j] >= DefOne && m.GridSave[i][j] <= DefEight {
					flagPos, style := m.AroundCount(i, j, false)
					if style == 0 {
						// 记录本次遍历所有无法确定雷的点
						// 这些点将进行更深层次的计算
						m.NotSurePos = append(m.NotSurePos, [2]int{i, j})
					} else {
						switch style {
						case 1: /* 类型1表示周围可点击格子全是雷 */
							for _, v := range flagPos {
								if m.TeachMode {
									fmt.Printf("坐标:(V %d,> %d, %s),原因:可点击个数 + 小旗个数 = 本格子雷数,right单击:(V %d,> %d)\n",
										i, j, m.GridSave[i][j], v[1], v[0])
								}
								m.ClickPos(v[0], v[1], "right")
							}
							/* 标完雷,此位置已无效 */
							m.GridSave[i][j] = DefNotNeed
						case 2: /* 类型2表示本格可以点鼠标中键了 */
							if m.TeachMode {
								fmt.Printf("原因:小旗个数 = 本格子雷数,center单击(V %d,> %d, %s)\n",
									i, j, m.GridSave[i][j])
							}
							m.ClickPos(j, i, "center")
							/* 点开一片区域,此位置已无效 */
							m.GridSave[i][j] = DefNotNeed
							/* 点中键后需要刷新当前界面避免重复点击中键 */
							if err = m.RefreshGrid(); err != nil {
								return err
							}
						case 3: /* 周围地雷已经标完,且不需要点击中键 */
							/* 点开一片区域,此位置已无效 */
							m.GridSave[i][j] = DefNotNeed
						}
						notSureFlag = false
					}
				}
			} // GameWide
		} // GameHigh

		if notSureFlag {
			// 上面的遍历得到全是不确定点,进入更深层次的判断
			if !m.StartFlag {
				// 还没开局,包括没有处理过点的情况,此时随机点格子
				m.ClickPos(rand.Intn(GameWide), rand.Intn(GameHigh), "left")
				continue
			}

			cnt := 0 // 遍历不确定的点,统计可以处理的点
			for _, v := range m.NotSurePos {
				if m.AroundNotSureCount(v[0], v[1]) {
					break // 如果对雷区有操作则需要重新整个界面扫雷
				}
				cnt++
			}

			if cnt == len(m.NotSurePos) {
				if m.ManualMine {
					fmt.Println("请您手动猜雷吧,鼠标点开或右键标雷...")
					// 此时需要等待鼠标左键或右键事件,触发后继续扫雷
					m.ReadKey.WaitKeyboard(win.VK_LBUTTON, win.VK_RBUTTON)
					m.AlreadyGuessed = true
				} else {
					/** 猜测某个点没有雷,运气成分,如需提高胜率可优化下面代码,下面是关于猜雷的思路
					 * 1.(https://tieba.baidu.com/p/1761431400?red_tag=3267954760)
					 *   (https://zhuanlan.zhihu.com/p/35974785)
					 *   上面是我看到比较靠谱的理论,由于计算机虽然笨但运算快,因此我打算实现这个方案
					 * 2.死猜,及无论如何都不可能判定哪个是雷,那就只能随机猜一个了
					 * 3.这里还要注意一点,及剩余雷数,有时候根据剩余雷数可以提高胜率
					 * 按照上面3个步骤,还没发确定是不是雷,妈逼只能靠运气了
					 * 到处找教程最终没能找到一个好点的方案,还是随机点击一个位置
					 **/
					/* 自动猜雷是随机点一个 */
					i := rand.Intn(cnt) // 下面是最挫的猜雷方案,随机找一个不确定点,在该点周围随机点一个点
					tPos, _ := m.AroundCount(m.NotSurePos[i][0], m.NotSurePos[i][1], true)
					i = rand.Intn(len(tPos)) // 在不确定列表中随机找一个点,随机点这个点周围的一个可点击点
					fmt.Printf("自动猜雷,left单击(V %d,> %d)\n",
						tPos[i][1], tPos[i][0])
					m.ClickPos(tPos[i][0], tPos[i][1], "left")
				}
			}
		} else {
			// 已经处理过确定的格子,已开局
			m.StartFlag = true
		}
	}
}

/*AroundCount
获取x,y点周围的可点击点情况
以及确定一些根据当前点就可以完成的情况
*/
func (m *Mine) AroundCount(x, y int, retMineCnt bool) (Around [][2]int, status int) {
	var (
		cntClick GridDefine // 标记可点击
		cntFlag  GridDefine // 标记红旗
	)
	// 双层循环遍历本点四周的点
	for i := x - 1; i <= x+1; i++ {
		for j := y - 1; j <= y+1; j++ {
			// 剔除超过边界点,以及x,y所在点
			if i >= 0 && j >= 0 && i < GameHigh && j < GameWide && (i != x || j != y) {
				switch m.GridSave[i][j] {
				case DefClick:
					Around = append(Around, [2]int{j, i})
					cntClick++
				case DefFlag:
					cntFlag++
				}
			}
		} // y
	} // x

	if retMineCnt {
		/* 返回周围可点击点位置,并且返回当前点剩余待标雷个数 */
		status = int(m.GridSave[x][y] - cntFlag)
		return
	}

	if m.GridSave[x][y] == cntFlag {
		if cntClick == 0 {
			/* 如果可点击数量为空,则当前位置不需要点鼠标中键 */
			status = 3
			return
		}
		/* 小旗个数等于本格子雷数,点击鼠标中键 */
		status = 2
		return
	}

	if cntClick+cntFlag == m.GridSave[x][y] {
		/* 可点击 + 小旗 = 本格子雷数,表示可点击一定全是雷 */
		status = 1
		return
	}
	/* 剩下的情况一定是可点击格数大于本格剩余雷数 */
	return
}

// AroundNotSureCount 处理无法确定的点,主要用相邻两个点进行判断
// 返回true表示标过雷或者点过数字,返回false表示没有任何操作
func (m *Mine) AroundNotSureCount(x, y int) bool {
	/* 得到本点可点击位置,以及剩余雷数 */
	clickPos, needMine := m.AroundCount(x, y, true)

	for i := x - 1; i <= x+1; i++ {
		for j := y - 1; j <= y+1; j++ {
			if i >= 0 && j >= 0 && i < GameHigh && j < GameWide && // 确保不会越界
				(i == x && j != y || i != x && j == y) && // 只找上下左右这4个点
				(m.GridSave[i][j] >= DefOne && m.GridSave[i][j] <= DefEight) && // 该点也必须是数字
				m.PosInNotSure(i, j) { // 该点也必须在不确定列表中
				var (
					// 两个点交叉位置个数
					cntMix = 0
					// 在x,y点周围找到合适的相邻点,进入深层次的判断逻辑
					nowPos, nowMine = m.AroundCount(i, j, true)
					// 保存2组坐标点,分别为在A点不在B点,在B点不在A点的位置
					morePos [2][][2]int
					// 表示选择morePos中的哪一个
					posIndex int
					// 鼠标按键值
					clickKey string
					// 教学模式打印输出
					fmtString string
				)
				for _, v1 := range clickPos {
					posIndex = 0 // 这里该变量作为标记使用,避免定义太多变量了
					for _, v2 := range nowPos {
						if v1 == v2 {
							posIndex = 1
							cntMix++ // 记录相交的点个数
							break
						}
					}
					if posIndex == 0 {
						// 记录在clickPos中且不在nowPos中的点
						morePos[0] = append(morePos[0], v1)
					}
				} // range clickPos

				for _, v1 := range nowPos {
					posIndex = 0
					for _, v2 := range clickPos {
						if v1 == v2 {
							posIndex = 1
							break
						}
					}
					if posIndex == 0 {
						// 记录在nowPos中且不在clickPos中的点
						morePos[1] = append(morePos[1], v1)
					}
				} // range nowPos

				// 当赋值其他数据时,表示一定需要点击
				posIndex = -1
				// 因为只有nowMine == needMine才为left,设置默认值
				clickKey = "right"
				if nowMine == needMine {
					// 两个点待标雷个数相同
					if cntMix == len(clickPos) {
						// 表示一个点全部在相交位置,此时另一个点附近不相交的点只能是数字
						posIndex = 1
						if m.TeachMode {
							fmtString = fmt.Sprintf("A:(V %d,> %d, %s)可点击数=相交个数(%d),B:(V %d,> %d, %s) B除相交点都是数字",
								x, y, m.GridSave[x][y], cntMix, i, j, m.GridSave[i][j])
						}
					} else if cntMix == len(nowPos) {
						// 同上,只是换了一个点而已
						posIndex = 0
						if m.TeachMode {
							fmtString = fmt.Sprintf("A:(V %d,> %d, %s)可点击数=相交个数(%d),B:(V %d,> %d, %s) B除相交点都是数字",
								i, j, m.GridSave[i][j], cntMix, x, y, m.GridSave[x][y])
						}
					}
					clickKey = "left"
				} else if nowMine > needMine {
					// 待标雷个数大的一方
					if nowMine-needMine == len(nowPos)-cntMix {
						// 2个点待标雷数相减 = 可点击数大的点减去重合点的个数,表示可点击数多的点多出的位置一定全是雷
						posIndex = 1
						if m.TeachMode {
							fmtString = fmt.Sprintf("A:(V %d,> %d, %s)待标雷数(%d) - B:(V %d,> %d, %s)待标雷数(%d) = A:可点击数(%d) - 相交个数(%d) = %d,原因:A除了相交位置全是雷",
								i, j, m.GridSave[i][j], nowMine, x, y, m.GridSave[x][y], needMine, len(nowPos), cntMix, nowMine-needMine)
						}
					}
				} else if needMine-nowMine == len(clickPos)-cntMix {
					posIndex = 0 // 同上,只是点不一样而已
					if m.TeachMode {
						fmtString = fmt.Sprintf("A:(V %d,> %d, %s)待标雷数(%d) - B:(V %d,> %d, %s)待标雷数(%d) = A:可点击数(%d) - 相交个数(%d) = %d,原因:A除了相交位置全是雷",
							x, y, m.GridSave[x][y], needMine, i, j, m.GridSave[i][j], nowMine, len(clickPos), cntMix, needMine-nowMine)
					}
				}

				if posIndex >= 0 && len(morePos[posIndex]) > 0 {
					// 操作本次根据相邻两点得出的步骤
					for _, v := range morePos[posIndex] {
						if m.TeachMode {
							fmt.Printf("%s,%s单击(V %d,> %d)\n", fmtString, clickKey, v[1], v[0])
						}
						// 需要操作的格子不是地雷则随便搞
						m.ClickPos(v[0], v[1], clickKey)
					}
					// 已经点击数字或标雷,整个界面需要重新判定
					return true
				}
			}
		}
	}
	return false
}

func (m *Mine) PosInNotSure(x, y int) bool {
	for _, v := range m.NotSurePos {
		if v[0] == x && v[1] == y {
			return true
		}
	}
	return false
}

func (m *Mine) ClickPos(x, y int, key string) {
	var NowPos = uintptr((x*GameGridLen + 21) | (y*GameGridLen+64)<<16)
	switch key {
	case "left": // 确定不是雷,则随便点左键
		win.SendMessage(m.GameHWND, win.WM_LBUTTONDOWN, 0, NowPos)
		win.SendMessage(m.GameHWND, win.WM_LBUTTONUP, 0, NowPos)
	case "right":
		if m.GridSave[y][x] == DefClick {
			/* 右键位置为可点击才点,否则不点,避免重复标雷 */
			win.SendMessage(m.GameHWND, win.WM_RBUTTONDOWN, 0, NowPos)
			win.SendMessage(m.GameHWND, win.WM_RBUTTONUP, 0, NowPos)
			m.GridSave[y][x] = DefFlag // 并且此处标记为地雷
		}
	case "center": // 中键点开一片区域
		win.SendMessage(m.GameHWND, win.WM_MBUTTONDOWN, 0, NowPos)
		win.SendMessage(m.GameHWND, win.WM_MBUTTONUP, 0, NowPos)
	}

	if m.TeachMode && m.StartFlag {
		/* 开局以后的操作才进入教学模式显示 */
		tmpPos := win.POINT{X: int32(x*GameGridLen + 21), Y: int32(y*GameGridLen + 64)}
		/* 将相对窗体位置转化为相对整个屏幕的位置 */
		win.ClientToScreen(m.GameHWND, &tmpPos)
		win.SetCursorPos(tmpPos.X, tmpPos.Y)
		fmt.Println("按空格继续教学模式...")
		// 等待空格键按下并松开
		m.ReadKey.WaitKeyboard(win.VK_SPACE)
	}
}

// -----------------------------------------------------------------------------

/*GridDefine 定义格子内容的类型 */
type GridDefine uint8

const ( /* 枚举类型,标记格子内容 */
	DefClick   GridDefine = iota // 可点击白板
	DefOne                       // 1
	DefTwo                       // 2
	DefThree                     // 3
	DefFour                      // 4
	DefFive                      // 5
	DefSix                       // 6
	DefSeven                     // 7
	DefEight                     // 8
	DefFlag                      // 红旗
	DefNotNeed                   // 不可点击的空白,以及标识无用的数字位置
	DefMine                      // 地雷
	DefRedMine                   // 标红地雷,表示输了
)

func (d GridDefine) String() string {
	switch d {
	case DefClick:
		return "可点击空白"
	case DefOne:
		return "数字1"
	case DefTwo:
		return "数字2"
	case DefThree:
		return "数字3"
	case DefFour:
		return "数字4"
	case DefFive:
		return "数字5"
	case DefSix:
		return "数字6"
	case DefSeven:
		return "数字7"
	case DefEight:
		return "数字8"
	case DefFlag:
		return "小旗子"
	case DefNotNeed:
		return "不可点击空白"
	case DefMine:
		return "地雷"
	case DefRedMine:
		return "标红地雷"
	default:
		return "未知"
	}
}

var (
	GridNumbers = map[string]GridDefine{
		"0101111111": DefOne,     // 1
		"0000000101": DefTwo,     // 2
		"0100000101": DefThree,   // 3
		"0101010111": DefFour,    // 4
		"0100101101": DefFive,    // 5
		"0000111111": DefSix,     // 6
		"1101010101": DefSeven,   // 7
		"0000010101": DefEight,   // 8
		"0111111110": DefFlag,    // 红旗
		"0101010101": DefMine,    // 地雷
		"0000000000": DefRedMine, // 标红地雷,表示输了
		"1111111110": DefClick,   // 可点击白板
		"1111111111": DefNotNeed, // 不可点击
	}
	/*
	 *   __0__
	 * 5|     |1
	 *  |__6__|
	 * 4|     |2
	 *  |__3__|
	 * 按照上面的顺序标记一个数字
	 * 相邻颜色值相同则赋值为1,不同则赋值为0
	 * 根据对应的map匹配得到该位置具体数字
	 **/
	NumberFlag = map[string]int{
		"1111110": 0,
		"0110000": 1,
		"1101101": 2,
		"1111001": 3,
		"0110011": 4,
		"1011011": 5,
		"1011111": 6,
		"1110000": 7,
		"1111111": 8,
		"1111011": 9,
	}
	WinErr  = errors.New("win")
	FailErr = errors.New("fail")
)

/*RefreshGrid
通过获取屏幕截图得到扫雷区域的数据
下面展示扫雷界面所有情况的灰度像素数据
可以根据这些数据找到特征值

数字1
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,0,0,1,1,1,1,1,1,0,
1,1,1,1,1,1,0,0,0,1,1,1,1,1,1,0,
1,1,1,1,1,0,0,0,0,1,1,1,1,1,1,0,
1,1,1,1,0,0,0,0,0,1,1,1,1,1,1,0,
1,1,1,1,1,1,0,0,0,1,1,1,1,1,1,0,
1,1,1,1,1,1,0,0,0,1,1,1,1,1,1,0,
1,1,1,1,1,1,0,0,0,1,1,1,1,1,1,0,
1,1,1,1,1,1,0,0,0,1,1,1,1,1,1,0,
1,1,1,1,0,0,0,0,0,0,0,1,1,1,1,0,
1,1,1,1,0,0,0,0,0,0,0,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,

数字2
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
1,1,1,0,0,0,0,0,0,0,0,1,1,1,1,1,
1,1,0,0,0,0,0,0,0,0,0,0,1,1,1,1,
1,1,0,0,0,1,1,1,1,0,0,0,1,1,1,1,
1,1,1,1,1,1,1,1,1,0,0,0,1,1,1,1,
1,1,1,1,1,1,1,0,0,0,0,1,1,1,1,1,
1,1,1,1,1,0,0,0,0,0,1,1,1,1,1,1,
1,1,1,0,0,0,0,0,1,1,1,1,1,1,1,1,
1,1,0,0,0,0,1,1,1,1,1,1,1,1,1,1,
1,1,0,0,0,0,0,0,0,0,0,0,1,1,1,1,
1,1,0,0,0,0,0,0,0,0,0,0,1,1,1,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,

数字3
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,0,0,0,0,0,0,0,0,0,1,1,1,1,0,
1,1,0,0,0,0,0,0,0,0,0,0,1,1,1,0,
1,1,1,1,1,1,1,1,1,0,0,0,1,1,1,0,
1,1,1,1,1,1,1,1,1,0,0,0,1,1,1,0,
1,1,1,1,1,0,0,0,0,0,0,1,1,1,1,0,
1,1,1,1,1,0,0,0,0,0,0,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,0,0,0,1,1,1,0,
1,1,1,1,1,1,1,1,1,0,0,0,1,1,1,0,
1,1,0,0,0,0,0,0,0,0,0,0,1,1,1,0,
1,1,0,0,0,0,0,0,0,0,0,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,

数字4
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,0,0,0,1,0,0,0,1,1,1,1,0,
1,1,1,1,0,0,0,1,0,0,0,1,1,1,1,0,
1,1,1,0,0,0,1,1,0,0,0,1,1,1,1,0,
1,1,1,0,0,0,1,1,0,0,0,1,1,1,1,0,
1,1,0,0,0,0,0,0,0,0,0,0,1,1,1,0,
1,1,0,0,0,0,0,0,0,0,0,0,1,1,1,0,
1,1,1,1,1,1,1,1,0,0,0,1,1,1,1,0,
1,1,1,1,1,1,1,1,0,0,0,1,1,1,1,0,
1,1,1,1,1,1,1,1,0,0,0,1,1,1,1,0,
1,1,1,1,1,1,1,1,0,0,0,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,

数字5
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
1,1,0,0,0,0,0,0,0,0,0,0,1,1,1,1,
1,1,0,0,0,0,0,0,0,0,0,0,1,1,1,1,
1,1,0,0,0,1,1,1,1,1,1,1,1,1,1,1,
1,1,0,0,0,1,1,1,1,1,1,1,1,1,1,1,
1,1,0,0,0,0,0,0,0,0,0,1,1,1,1,1,
1,1,0,0,0,0,0,0,0,0,0,0,1,1,1,1,
1,1,1,1,1,1,1,1,1,0,0,0,1,1,1,1,
1,1,1,1,1,1,1,1,1,0,0,0,1,1,1,1,
1,1,0,0,0,0,0,0,0,0,0,0,1,1,1,1,
1,1,0,0,0,0,0,0,0,0,0,1,1,1,1,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,

数字6
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,0,0,0,0,0,0,0,0,1,1,1,1,0,
1,1,0,0,0,0,0,0,0,0,0,1,1,1,1,0,
1,1,0,0,0,1,1,1,1,1,1,1,1,1,1,0,
1,1,0,0,0,1,1,1,1,1,1,1,1,1,1,0,
1,1,0,0,0,0,0,0,0,0,0,1,1,1,1,0,
1,1,0,0,0,0,0,0,0,0,0,0,1,1,1,0,
1,1,0,0,0,1,1,1,1,0,0,0,1,1,1,0,
1,1,0,0,0,1,1,1,1,0,0,0,1,1,1,0,
1,1,0,0,0,0,0,0,0,0,0,0,1,1,1,0,
1,1,1,0,0,0,0,0,0,0,0,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,

数字7
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,0,0,0,0,0,0,0,0,0,0,1,1,1,0,
1,1,0,0,0,0,0,0,0,0,0,0,1,1,1,0,
1,1,1,1,1,1,1,1,1,0,0,0,1,1,1,0,
1,1,1,1,1,1,1,1,1,0,0,0,1,1,1,0,
1,1,1,1,1,1,1,1,0,0,0,1,1,1,1,0,
1,1,1,1,1,1,1,1,0,0,0,1,1,1,1,0,
1,1,1,1,1,1,1,0,0,0,1,1,1,1,1,0,
1,1,1,1,1,1,1,0,0,0,1,1,1,1,1,0,
1,1,1,1,1,1,0,0,0,1,1,1,1,1,1,0,
1,1,1,1,1,1,0,0,0,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,

数字8
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,0,0,0,0,0,0,0,0,1,1,1,1,0,
1,1,0,0,0,0,0,0,0,0,0,0,1,1,1,0,
1,1,0,0,0,1,1,1,1,0,0,0,1,1,1,0,
1,1,0,0,0,1,1,1,1,0,0,0,1,1,1,0,
1,1,1,0,0,0,0,0,0,0,0,1,1,1,1,0,
1,1,1,0,0,0,0,0,0,0,0,1,1,1,1,0,
1,1,0,0,0,1,1,1,1,0,0,0,1,1,1,0,
1,1,0,0,0,1,1,1,1,0,0,0,1,1,1,0,
1,1,0,0,0,0,0,0,0,0,0,0,1,1,1,0,
1,1,1,0,0,0,0,0,0,0,0,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,

红色旗子
1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,0,0,0,
1,1,1,1,1,1,0,0,1,1,1,1,1,0,0,0,
1,1,1,1,0,0,0,0,1,1,1,1,1,0,0,0,
1,1,1,0,0,0,0,0,1,1,1,1,1,0,0,0,
1,1,1,1,0,0,0,0,1,1,1,1,1,0,0,0,
1,1,1,1,1,1,0,0,1,1,1,1,1,0,0,0,
1,1,1,1,1,1,1,0,1,1,1,1,1,0,0,0,
1,1,1,1,1,1,1,0,1,1,1,1,1,0,0,0,
1,1,1,1,1,0,0,0,0,1,1,1,1,0,0,0,
1,1,1,0,0,0,0,0,0,0,0,1,1,0,0,0,
1,1,1,0,0,0,0,0,0,0,0,1,1,0,0,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,0,0,0,
1,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,

地雷
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
1,1,1,1,1,1,1,0,1,1,1,1,1,1,1,1,
1,1,1,1,1,1,1,0,1,1,1,1,1,1,1,1,
1,1,1,0,1,0,0,0,0,0,1,0,1,1,1,1,
1,1,1,1,0,0,0,0,0,0,0,1,1,1,1,1,
1,1,1,0,0,1,1,0,0,0,0,0,1,1,1,1,
1,1,1,0,0,1,1,0,0,0,0,0,1,1,1,1,
1,0,0,0,0,0,0,0,0,0,0,0,0,0,1,1,
1,1,1,0,0,0,0,0,0,0,0,0,1,1,1,1,
1,1,1,0,0,0,0,0,0,0,0,0,1,1,1,1,
1,1,1,1,0,0,0,0,0,0,0,1,1,1,1,1,
1,1,1,0,1,0,0,0,0,0,1,0,1,1,1,1,
1,1,1,1,1,1,1,0,1,1,1,1,1,1,1,1,
1,1,1,1,1,1,1,0,1,1,1,1,1,1,1,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,

标红地雷
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,
0,0,0,0,0,1,1,0,0,0,0,0,0,0,0,1,
0,0,0,0,0,1,1,0,0,0,0,0,0,0,0,1,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,

可点击白板
1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,0,0,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,0,0,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,0,0,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,0,0,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,0,0,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,0,0,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,0,0,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,0,0,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,0,0,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,0,0,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,0,0,1,
1,1,1,1,1,1,1,1,1,1,1,1,1,0,0,1,
1,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,

不可点击白板
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,0,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,

截屏代码摘自 https://github.com/vova616/screenshot
*/
func (m *Mine) RefreshGrid() error {
	if m.StartFlag && (m.TeachMode || m.AlreadyGuessed) {
		// 开局 && (教学模式 || 刚猜雷一次)
		// 将鼠标指针移动到笑脸位置,避免截屏数据包含鼠标指针
		win.SetCursorPos(m.StartBtn.X, m.StartBtn.Y)
		time.Sleep(time.Millisecond * 200)
		// 后面非猜雷步骤无需每次都移动指针
		m.AlreadyGuessed = false
	}

	// 截取雷区所有像素数据,13/56,是经过调试的特殊值
	w, h := GameWide*GameGridLen, GameHigh*GameGridLen
	err := m.GetImg(13, 56, int32(w), int32(h), func(data []byte) error {
		// 将rgb转换为0/1的灰度数据
		grayControl := func(data []byte, index int) byte {
			r, g, b := data[index+2], data[index+1], data[index]
			if float32(r)*0.11+float32(g)*0.59+float32(b)*0.3 >= 150 {
				return '1'
			}
			return '0'
		}

		tmpArr := make([]byte, 10)
		for a := 0; a < GameHigh; a++ { // 遍历高度
			for b := 0; b < GameWide; b++ { // 遍历宽度
				if DefNotNeed == m.GridSave[a][b] || DefFlag == m.GridSave[a][b] {
					continue /* 该点已没意义 或 该点已经标记为地雷,所以不用计算 */
				}

				for c, e := 0, 0; c < 5; c++ {
					// 找到特殊的10个点的灰度图数据,用于匹配特征值,确定这个格子表示的值
					tmpArr[e] = grayControl(data, (a*GameGridLen+7-c)*4*GameWide*GameGridLen+4*(b*GameGridLen+7+c))
					e++
					tmpArr[e] = grayControl(data, (a*GameGridLen+9+c)*4*GameWide*GameGridLen+4*(b*GameGridLen+2))
					e++
				}

				k, ok := GridNumbers[string(tmpArr)]
				if ok {
					if k == DefMine || k == DefRedMine {
						/* 遇到标红的雷或者黑色的雷,游戏结束 */
						return errors.Wrapf(FailErr, "Game Over,坐标:(V %d,> %d, %s)", a, b, k)
					}
					m.GridSave[a][b] = k // 找到匹配特征值,进行赋值
				} else {
					return errors.Errorf("cannot resolve: %s", tmpArr)
				}
			} // 内层for结束
		} // 外层for结束
		return nil
	})
	if err != nil {
		return err
	}

	w, h = 36, 20 // 只截取左上角的剩余雷数图片,18/17/36/20,已经过计算
	return m.GetImg(18, 17, int32(w), int32(h), func(data []byte) error {
		// cmpPix 相邻两个点的rgb值一样表示数字红色,否则为阴影
		cmpPix := func(data []byte, a, b int) byte {
			if data[a] == data[b] &&
				data[a+1] == data[b+1] &&
				data[a+2] == data[b+2] {
				return '1'
			}
			return '0'
		}

		m.CountMine = 0
		flagCnt := make([]byte, 7)
		// 遍历每个数字右上角X方向偏移
		for i, v := range [3]int{0, 13, 26} {
			//  (y)*4*w + (x)*4, 将特殊点和同列下一行点进行比较
			index := (v + 5) * 4
			flagCnt[0] = cmpPix(data, index, index+4*w)
			index = 4*4*w + (v+9)*4
			flagCnt[1] = cmpPix(data, index, index+4*w)
			index = 14*4*w + (v+9)*4
			flagCnt[2] = cmpPix(data, index, index+4*w)
			index = 18*4*w + (v+5)*4
			flagCnt[3] = cmpPix(data, index, index+4*w)
			index = 14*4*w + (v+1)*4
			flagCnt[4] = cmpPix(data, index, index+4*w)
			index = 4*4*w + (v+1)*4
			flagCnt[5] = cmpPix(data, index, index+4*w)
			index = 9*4*w + (v+5)*4
			flagCnt[6] = cmpPix(data, index, index+4*w)

			n, ok := NumberFlag[string(flagCnt)]
			if ok {
				for i = 2 - i; i > 0; i-- {
					n *= 10
				}
				m.CountMine += n
			} else {
				return errors.Errorf("cannot resolve: %s", flagCnt)
			}
		}
		if m.CountMine == 0 {
			// 赢了比赛
			return WinErr
		}
		return nil
	})
}

// GetImg 截取扫雷窗口图片,将数据提供该回调方法内进行处理
func (m *Mine) GetImg(sx, sy, w, h int32, f func([]byte) error) error {
	hdc := win.GetDC(m.GameHWND)
	if hdc == 0 {
		return errors.New("win.GetDC")
	}
	defer win.ReleaseDC(m.GameHWND, hdc)

	hdcM := win.CreateCompatibleDC(hdc)
	if hdcM == 0 {
		return errors.New("win.CreateCompatibleDC")
	}
	defer win.DeleteDC(hdcM)

	var (
		bitSize = int(w * h * 4)
		bi      win.BITMAPINFO
	)
	bi.BmiHeader.BiSize = uint32(reflect.TypeOf(bi.BmiHeader).Size())
	bi.BmiHeader.BiWidth = w
	bi.BmiHeader.BiHeight = -h /* Non-cartesian, please */
	bi.BmiHeader.BiPlanes = 1
	bi.BmiHeader.BiBitCount = 32
	bi.BmiHeader.BiCompression = win.BI_RGB
	bi.BmiHeader.BiSizeImage = uint32(bitSize)

	//goland:noinspection GoVetUnsafePointer
	ptr := unsafe.Pointer(uintptr(0))
	dib := win.CreateDIBSection(hdc, &bi.BmiHeader, win.DIB_RGB_COLORS, &ptr, 0, 0)
	if dib == 0 {
		return errors.New("win.CreateDIBSection")
	}
	if win.GpStatus(dib) == win.InvalidParameter {
		return errors.New("win.InvalidParameter")
	}
	defer win.DeleteObject(win.HGDIOBJ(dib))

	obj := win.SelectObject(hdcM, win.HGDIOBJ(dib))
	if obj == 0 {
		return errors.New("win.SelectObject == 0")
	}
	if obj == 0xffffffff {
		return errors.New("win.SelectObject == 0xffffffff")
	}
	defer win.DeleteObject(obj)

	// 下面的13,56是经过计算,相对于扫雷窗体左上角坐标的偏移,拿到
	if !win.BitBlt(hdcM, 0, 0, w, h, hdc, sx, sy, win.SRCCOPY) {
		return errors.New("win.BitBlt")
	}

	var slice []byte
	hDrp := (*reflect.SliceHeader)(unsafe.Pointer(&slice))
	hDrp.Data = uintptr(ptr)
	hDrp.Len, hDrp.Cap = bitSize, bitSize

	return f(slice)
}

// DoubleClickStart 双击开始按钮重新开局
func (m *Mine) DoubleClickStart() {
	win.SetCursorPos(m.StartBtn.X, m.StartBtn.Y)

	lz := syscall.NewLazyDLL("user32.dll").NewProc("mouse_event")
	_, _, _ = lz.Call(6)
	time.Sleep(time.Millisecond * 200)
	_, _, _ = lz.Call(6)
	time.Sleep(time.Millisecond * 200)
}

type KeyBoard struct {
	sync.Mutex
	cache  map[int32][]chan int32
	notice chan []int32
}

func NewKeyBoard() *KeyBoard {
	k := &KeyBoard{
		cache:  make(map[int32][]chan int32),
		notice: make(chan []int32, 1),
	}
	go k.HandleKeyBoard()
	return k
}

/*WaitKeyboard
传入等待按键值,当触发其中一个按键时返回该按键值

该方法线程安全,可同时为多个协程监听按键
下方代码当按下'C'时,三个协程都会触发
func main() {
	wg := sync.WaitGroup{}
	rk := NewKeyBoard()
	for {
		wg.Add(3)
		go func() {
			i := rk.WaitKeyboard('A', 'B', 'C')
			fmt.Printf("1,%c\n", i)
			wg.Done()
		}()
		go func() {
			i := rk.WaitKeyboard('B', 'C', 'D')
			fmt.Printf("2,%c\n", i)
			wg.Done()
		}()
		go func() {
			i := rk.WaitKeyboard('D', 'E', 'C')
			fmt.Printf("3,%c\n", i)
			wg.Done()
		}()
		wg.Wait()
	}
}*/
func (k *KeyBoard) WaitKeyboard(key ...int32) int32 {
	var (
		ch     = make(chan int32)
		newKey []int32
	)
	k.Lock()
	for _, v := range key {
		if len(k.cache[v]) == 0 {
			// 新注册的按键,创建新的chan数组
			k.cache[v] = []chan int32{ch}
			newKey = append(newKey, v)
		} else {
			// 该按键已经注册,将chan添加到数组
			k.cache[v] = append(k.cache[v], ch)
		}
	}
	k.Unlock()

	if len(newKey) > 0 {
		// 存在新注册的key,通知捕获按键,必须晚于cache的设置
		k.notice <- newKey
	}
	return <-ch
}

func (k *KeyBoard) HandleKeyBoard() {
	waitKey := func(key int32) {
		// 等待按键按下
		for win.GetKeyState(key) >= 0 {
			time.Sleep(time.Millisecond * 100)
		}
		// 等待按键松开
		for win.GetKeyState(key) < 0 {
			time.Sleep(time.Millisecond * 100)
		}

		k.Lock()
		// 为所有注册该按键的chan发送消息
		for _, ch := range k.cache[key] {
			select {
			case ch <- key:
				// 忽略阻塞chan,该ch可能已响应其他按键
			default:
			}
		}
		// 该按键已经被消费,从map中删除
		// 当有新注册的该按键出现,则继续消费
		delete(k.cache, key)
		k.Unlock()
	}

	// 接收ch中新增的监听key
	for c := range k.notice {
		for _, v := range c {
			go waitKey(v)
		}
	}
}
